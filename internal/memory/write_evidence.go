package memory

import (
	"context"
	"strings"
)

// userTurnKey is private so model tool arguments cannot manufacture the
// user-authored turn used to authorize durable memory promotion.
type userTurnKey struct{}

func WithUserTurn(ctx context.Context, text string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, userTurnKey{}, text)
}

type WriteSource struct {
	Type  string
	Ref   string
	Quote string
}

// durableWriteSource only marks a tool write as explicit when the frozen user
// turn is a memory instruction whose payload is exactly the new value.
// Ambiguous paraphrases remain candidates for review.
func durableWriteSource(ctx context.Context, value string) (WriteSource, bool) {
	if ctx == nil {
		return WriteSource{Type: "agent_candidate"}, false
	}
	turn, _ := ctx.Value(userTurnKey{}).(string)
	turn = strings.TrimSpace(turn)
	request := strings.ToLower(turn)
	explicit := false
	value = strings.TrimSpace(value)
	for _, prefix := range []string{"记住", "请记住", "帮我记住", "请帮我记住", "please remember", "remember", "save to memory", "请保存为长期记忆"} {
		if strings.HasPrefix(request, prefix) {
			payload := strings.TrimLeft(strings.TrimSpace(turn[len(prefix):]), "：:= \t\r\n")
			explicit = value != "" && payload == value
			break
		}
	}
	source := WriteSource{Type: "agent_candidate"}
	if explicit {
		source.Type = "user_explicit"
	}
	if value != "" && strings.Contains(turn, value) {
		runes := []rune(value)
		if len(runes) > 300 {
			runes = runes[:300]
		}
		source.Quote = string(runes)
	}
	if ctx != nil {
		source.Ref, _ = ctx.Value("task_id").(string)
		if len([]rune(source.Ref)) > 128 {
			source.Ref = memoryHash(source.Ref)
		}
	}
	return source, explicit
}

// Confirmation is a narrow command, not a substring search over prose. Extra
// clauses remain ambiguous and must not authorize another candidate.
func explicitCandidateConfirmation(ctx context.Context, key string) bool {
	if ctx == nil || strings.TrimSpace(key) == "" {
		return false
	}
	turn, _ := ctx.Value(userTurnKey{}).(string)
	turn = strings.TrimSpace(turn)
	for _, prefix := range []string{"确认记忆", "请确认记忆", "confirm memory", "please confirm memory"} {
		if !strings.HasPrefix(strings.ToLower(turn), prefix) {
			continue
		}
		tail := turn[len(prefix):]
		if strings.HasSuffix(prefix, "memory") && tail != "" && !strings.ContainsRune("：: \t\r\n", []rune(tail)[0]) {
			continue
		}
		payload := strings.TrimSpace(strings.TrimLeft(tail, "：: \t\r\n"))
		// Allow paired quoting, while keeping key spelling and case exact.
		for _, pair := range [][2]string{{"`", "`"}, {"\"", "\""}, {"'", "'"}, {"“", "”"}} {
			if strings.HasPrefix(payload, pair[0]) && strings.HasSuffix(payload, pair[1]) && len(payload) >= len(pair[0])+len(pair[1]) {
				payload = payload[len(pair[0]) : len(payload)-len(pair[1])]
				break
			}
		}
		return payload == key
	}
	return false
}
