// Package mcpserver 는 표준 입출력으로 JSON-RPC 2.0 메시지를 한 줄에 하나씩 주고받는 MCP 서버다.
// 도구는 heka_find, heka_add, heka_flag 셋이다. 검토는 사람이 시작하므로 MCP 에 두지 않는다. 도구 호출이 어떻게 실패해도 서버는 죽지 않는다.
package mcpserver

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/hee-develop/heka/internal/config"
	"github.com/hee-develop/heka/internal/index"
	"github.com/hee-develop/heka/internal/lookup"
	"github.com/hee-develop/heka/internal/register"
)

// versions 는 이 서버가 말할 수 있는 프로토콜 판이다(initialize 방식). 앞이 가장 새 판이다.
var versions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

// JSON-RPC 오류 번호.
const (
	errParse    = -32700
	errRequest  = -32600
	errMethod   = -32601
	errParams   = -32602
	errInternal = -32603
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Server 는 데이터 폴더 하나를 다루는 서버다.
type Server struct {
	Home    string
	Version string
	Now     func() time.Time
}

// Serve 는 r 에서 한 줄씩 읽어 처리하고 w 에 한 줄씩 응답한다. r 이 끝나면 돌아온다.
func (s *Server) Serve(r io.Reader, w io.Writer) error {
	br := bufio.NewReader(r)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	for {
		line, rerr := br.ReadBytes('\n')
		if line = bytes.TrimSpace(line); len(line) > 0 {
			if resp := s.handle(line); resp != nil {
				if err := enc.Encode(resp); err != nil {
					return err
				}
			}
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				return nil
			}
			return rerr
		}
	}
}

func fail(id json.RawMessage, code int, msg string) *response {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return &response{JSONRPC: "2.0", ID: id, Error: &rpcError{code, msg}}
}

// handle 은 메시지 한 줄을 처리한다. 응답이 없어야 하면(알림) nil 이다.
func (s *Server) handle(line []byte) (resp *response) {
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		return fail(nil, errParse, "JSON 을 해석하지 못함: "+err.Error())
	}
	if req.Method == "" {
		if len(req.ID) > 0 && req.JSONRPC == "2.0" {
			return nil // 클라이언트가 보낸 응답은 받을 일이 없다
		}
		return fail(req.ID, errRequest, "method 가 없음")
	}
	notification := len(req.ID) == 0
	defer func() {
		if r := recover(); r != nil {
			resp = fail(req.ID, errInternal, fmt.Sprint("내부 오류: ", r))
			if notification {
				resp = nil
			}
		}
	}()
	var result any
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		v := versions[0]
		if slices.Contains(versions, p.ProtocolVersion) {
			v = p.ProtocolVersion
		}
		result = map[string]any{
			"protocolVersion": v,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "heka", "version": s.Version},
		}
	case "ping":
		result = map[string]any{}
	case "tools/list":
		result = map[string]any{"tools": tools}
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.Name == "" {
			return fail(req.ID, errParams, "tools/call 의 params 에 name 이 필요함")
		}
		if !slices.ContainsFunc(tools, func(t tool) bool { return t.Name == p.Name }) {
			return fail(req.ID, errParams, "알 수 없는 도구: "+p.Name)
		}
		result = s.call(p.Name, p.Arguments)
	default:
		if notification {
			return nil // 모르는 알림은 조용히 넘긴다
		}
		return fail(req.ID, errMethod, "알 수 없는 메서드: "+req.Method)
	}
	if notification {
		return nil
	}
	return &response{JSONRPC: "2.0", ID: req.ID, Result: result}
}

func text(msg string, isErr bool) map[string]any {
	return map[string]any{"content": []map[string]any{{"type": "text", "text": msg}}, "isError": isErr}
}

// call 은 도구 하나를 실행한다. 실패는 isError 가 든 결과로 돌려준다.
func (s *Server) call(name string, args json.RawMessage) (res map[string]any) {
	defer func() {
		if r := recover(); r != nil {
			res = text(fmt.Sprint("도구 실행 중 내부 오류: ", r), true)
		}
	}()
	msg, isErr, err := s.run(name, args)
	if err != nil {
		return text(err.Error(), true)
	}
	return text(msg, isErr)
}

func (s *Server) run(name string, args json.RawMessage) (msg string, isErr bool, err error) {
	if len(bytes.TrimSpace(args)) == 0 {
		args = json.RawMessage("{}")
	}
	cfg, err := config.Load(s.Home)
	if err != nil {
		return "", false, err
	}
	now := s.Now()
	env := register.Env{Home: s.Home, Cfg: cfg, Now: now}
	strict := func(v any) error {
		if err := json.Unmarshal(args, v); err != nil {
			return fmt.Errorf("인자를 해석하지 못함: %w", err)
		}
		return nil
	}
	switch name {
	case "heka_find":
		var a struct {
			Query  string   `json:"query"`
			Scopes []string `json:"scopes"`
			Budget int      `json:"budget"`
		}
		if err := strict(&a); err != nil {
			return "", false, err
		}
		if strings.TrimSpace(a.Query) == "" {
			return "", false, errors.New("query 가 필요함")
		}
		ix, err := s.open(cfg)
		if err != nil {
			return "", false, err
		}
		defer ix.Close()
		cwd, _ := os.Getwd()
		q := lookup.Query{Text: a.Query, Scopes: lookup.Scopes(cfg, a.Scopes, cwd), Budget: a.Budget, Today: now.Format("2006-01-02")}
		r, err := lookup.Find(ix, cfg, q)
		if err != nil {
			return "", false, err
		}
		lookup.Log(s.Home, q, r, now)
		return r.Text(), false, nil
	case "heka_add":
		var in register.Input
		if err := strict(&in); err != nil {
			return "", false, err
		}
		r, err := register.Add(env, in)
		if err != nil {
			return "", false, err
		}
		return r.Text(), r.Status == "invalid", nil
	case "heka_flag":
		var a struct{ ID, Reason string }
		if err := strict(&a); err != nil {
			return "", false, err
		}
		if a.ID == "" || strings.TrimSpace(a.Reason) == "" {
			return "", false, errors.New("id 와 reason 이 필요함")
		}
		if err := register.Flag(env, a.ID, a.Reason); err != nil {
			return "", false, err
		}
		return fmt.Sprintf("%s 에 표시를 남겼습니다. 항목 파일은 고치지 않았습니다. 사람이 점검 때 봅니다.", a.ID), false, nil
	}
	return "", false, fmt.Errorf("알 수 없는 도구: %s", name)
}

// open 은 색인이 낡았으면 다시 만들고 연다. 요청마다 부른다.
func (s *Server) open(cfg *config.Config) (*index.Index, error) {
	if err := index.Ensure(s.Home, cfg); err != nil {
		return nil, err
	}
	return index.Open(s.Home)
}
