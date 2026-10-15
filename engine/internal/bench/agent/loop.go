package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tamen25/Argus/engine/internal/clip"
)

// reply is one model turn, in a form both API adapters translate to.
type reply struct {
	// text is the turn's visible text, quoted in the error a run ends with
	// when the model never calls a tool.
	text string
	// calls are the tool calls the model asked for, in order.
	calls []toolUse
	// truncated says the turn stopped at the per-turn output cap.
	truncated bool
	// tokens is what the turn cost, input and output together.
	tokens int
}

// toolUse is one tool call the model asked for.
type toolUse struct {
	id, name string
	args     json.RawMessage
}

// toolResult answers one toolUse.
type toolResult struct {
	id, name, content string
}

// conversation is one provider's message list, in its own wire format. The
// API adapters implement it; runTurns drives it.
type conversation interface {
	// next sends the conversation so far and returns the model's reply.
	next(ctx context.Context) (reply, error)
	// keepReply appends the last reply as an assistant turn. A reply with
	// neither text nor tool calls is skipped: providers reject an empty turn.
	keepReply()
	// answerTools appends the results of the last reply's tool calls, and the
	// budget notice after them when there is one.
	answerTools(results []toolResult, notice string)
	// say appends a user message.
	say(text string)
}

// runTurns is the tool-use loop both API adapters share: everything about a
// run that is not wire format. In order, each turn:
//
//   - a model call that fails with a 5xx is repeated, at most maxRetries per
//     run, and is not another step;
//   - a turn with no tool call is reminded once to call submit_diagnosis
//     (noToolNotice), then fails the run; a turn cut off at the output cap
//     fails it at once;
//   - a submit_diagnosis call ends the run, even on a turn past the token
//     cap, unless reviewSubmission finds a problem with its arguments, which
//     the model is told so it can correct them;
//   - past a cap, or after the budget notice, any other tool call ends the
//     run with nothing executed, so a run capped at 12 tool calls never
//     reports 13;
//   - otherwise every call is answered (a call past the tool-call cap with a
//     refusal, since each call id needs an answer), followed by the budget
//     notice once the budget runs low.
//
// name labels the agent in errors; outputCap is the per-turn output cap, for
// the error a truncated turn ends with.
func runTurns(ctx context.Context, name string, task Task, conv conversation, outputCap int) (Result, error) {
	s := &session{budget: task.Budget}
	maxSteps := 20
	if task.Budget.MaxToolCalls > 0 {
		maxSteps = task.Budget.MaxToolCalls + 2 // room for the terminal submit and a final turn
	}

	for step := 0; step < maxSteps; step++ {
		s.usage.Steps++
		r, err := conv.next(ctx)
		if err != nil {
			if s.retry(err) {
				step-- // the same turn again: a repeated call is not another step
				s.usage.Steps--
				continue
			}
			return s.result(nil), err
		}
		s.addTokens(r.tokens)

		if len(r.calls) == 0 {
			if r.truncated {
				return s.result(nil), fmt.Errorf("agent %s: a turn reached the per-turn output cap (%d tokens) without calling a tool",
					name, outputCap)
			}
			if s.nudge() {
				conv.keepReply()
				conv.say(noToolNotice)
				continue
			}
			return s.result(nil), fmt.Errorf("agent %s: finished without calling %s (last reply: %q)",
				name, submitToolName, clip.Middle(r.text, maxLoggedReply))
		}

		rejected := map[string]string{}
		for _, c := range r.calls {
			if c.name != submitToolName {
				continue
			}
			problem := s.reviewSubmission(c.args, task.Categories)
			if problem == "" {
				return s.result(c.args), nil
			}
			rejected[c.id] = problem
		}
		if (s.overTokens() || s.warned) && len(rejected) == 0 {
			return s.result(nil), s.exhausted()
		}

		conv.keepReply()
		results := make([]toolResult, 0, len(r.calls))
		for _, c := range r.calls {
			content := budgetRefusal
			if problem, ok := rejected[c.id]; ok {
				content = submitRejection(problem)
			} else if !s.spent() && !s.warned {
				content = s.call(ctx, task.Tools, c.name, c.args)
			}
			results = append(results, toolResult{id: c.id, name: c.name, content: content})
		}
		conv.answerTools(results, s.notice())
	}
	return s.result(nil), fmt.Errorf("agent %s: exceeded %d steps without submitting", name, maxSteps)
}

// openingBrief is the first user message: the incident brief, the categories
// to choose from, how to answer, and the budget.
func openingBrief(task Task) string {
	return task.Brief + categoryBrief(task.Categories) + "\n\nWhen you have identified the root cause, call " +
		submitToolName + "." + budgetBrief(task.Budget)
}
