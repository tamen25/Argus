package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tamen25/Argus/engine/internal/mcp"
)

// sizedTools answers every call with n bytes.
type sizedTools struct{ n int }

func (s sizedTools) List() []mcp.Tool { return nil }

func (s sizedTools) Call(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(strings.Repeat("x", s.n)), nil
}

// R29: an answer larger than maxToolAnswer is cut before the model sees it,
// with a note saying how to narrow the query, and the tool log records the
// real size and the cut. A smaller answer passes untouched.
func TestToolAnswerIsCapped(t *testing.T) {
	s := &session{}
	small := s.call(context.Background(), sizedTools{n: 100}, "query_prometheus", nil)
	if len(small) != 100 || s.calls[0].Cut {
		t.Errorf("a 100-byte answer came back as %d bytes, cut %v", len(small), s.calls[0].Cut)
	}

	big := s.call(context.Background(), sizedTools{n: maxToolAnswer + 1000}, "query_prometheus", nil)
	rec := s.calls[1]
	if !rec.Cut || rec.ResultBytes != maxToolAnswer+1000 {
		t.Errorf("log = %+v, want the real size and Cut", rec)
	}
	if !strings.HasPrefix(big, strings.Repeat("x", maxToolAnswer)) || !strings.Contains(big, "Narrow the query") {
		t.Errorf("the model was not shown the first %d bytes and the note", maxToolAnswer)
	}
	if len(big) > maxToolAnswer+512 {
		t.Errorf("the model was shown %d bytes", len(big))
	}
}
