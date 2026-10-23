package llmpool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"log"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/Autumn-27/norma/llm"
)

// Member is one LLM profile in the chain, already built into a provider (wrapped
// with the recorder by the caller, so a failed attempt is still recorded under
// its own profile name).
type Member struct {
	ID       int64  // llm_profiles.id
	Name     string // profile name, for logs / UI
	Model    string
	Format   string // "anthropic" | "openai"
	Priority int    // the profile's configured priority (display only)
	Active   bool   // is_default (display only)
	// Rank is the ordering key the caller assigned: higher goes first, and members
	// sharing a Rank take turns leading (load-spreading across duplicate keys).
	// The caller encodes "active profile heads the chain" as a Rank above every
	// user-settable priority, so this type needs no policy of its own.
	Rank int
	// WindowTokens is the profile's context window in tokens. A member whose
	// window can't hold the request is skipped rather than made to fail on it.
	WindowTokens int
	Prov         llm.Provider
}

// RankActive is the Rank the caller gives the chain head (the active profile, or
// an explicitly bound one) so it always outranks any configured priority.
const RankActive = int(^uint(0)>>1) - 1

// ErrExhausted is returned when every member of the chain failed.
var ErrExhausted = errors.New("LLM 轮询：所有配置均不可用")

// Pool is an llm.Provider that fails over across an ordered chain of members.
// It is safe for concurrent use: members are immutable after construction and
// all mutable state lives in the shared Registry.
type Pool struct {
	members []*Member // in chain order (active first, then priority DESC)
	health  *Registry
	rr      atomic.Uint64 // rotates the starting point within an equal-priority group
}

// New builds a Pool over members (already in chain order). Returns nil when the
// chain is empty. A single-member chain is still a valid Pool — it just behaves
// exactly like the bare provider.
func New(members []*Member, health *Registry) *Pool {
	if len(members) == 0 {
		return nil
	}
	if health == nil {
		health = NewRegistry(nil, nil)
	}
	return &Pool{members: members, health: health}
}

// Members returns the chain in order (read-only).
func (p *Pool) Members() []*Member { return p.members }

// Head returns the first member of the chain.
func (p *Pool) Head() *Member { return p.members[0] }

// Stream implements llm.Provider with failover.
//
// The one hard rule: a member may only be abandoned BEFORE it has yielded any
// event. Once text or a tool_use has reached the caller, re-sending the same
// request to another model would duplicate output and corrupt the conversation
// history — so a mid-stream failure is surfaced as-is and left to the agent
// harness's resume logic. Fortunately the failures this exists for (402 no
// credit, 401 bad key, 429, 5xx) all surface during request establishment,
// before the body is read, so they always land in the safe window.
func (p *Pool) Stream(ctx context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	order := p.order(req)
	return func(yield func(llm.StreamEvent, error) bool) {
		var lastErr error
		for i, m := range order {
			emitted := false
			var failed error
			for ev, err := range m.Prov.Stream(ctx, req) {
				if err != nil && !emitted && shouldFailover(ctx, err) {
					failed = err
					break // safe window: nothing reached the caller yet
				}
				emitted = true
				if !yield(ev, err) {
					return // caller stopped consuming (cancel / early exit)
				}
				if err != nil {
					return // terminal error already handed to the caller
				}
			}
			if failed == nil {
				p.health.Pass(m.ID) // completed (or failed in a non-failover way)
				return
			}
			lastErr = failed
			hard := isHardFailure(failed)
			if p.health.Trip(m.ID, trimErr(failed), hard) {
				log.Printf("[llmpool] 配置 %q(%s) 已熔断：%s", m.Name, m.Model, trimErr(failed))
			}
			if i+1 < len(order) {
				n := order[i+1]
				log.Printf("[llmpool] LLM 故障转移：%q(%s) → %q(%s)，原因：%s",
					m.Name, m.Model, n.Name, n.Model, trimErr(failed))
			}
		}
		if lastErr == nil {
			lastErr = ErrExhausted
		}
		log.Printf("[llmpool] 轮询链已耗尽(%d 个配置全部失败)，最后错误：%s", len(order), trimErr(lastErr))
		yield(llm.StreamEvent{}, fmt.Errorf("%w：%v", ErrExhausted, lastErr))
	}
}

// Complete implements llm.Provider with failover for non-streaming calls. A
// non-streaming request is atomic — it never delivers partial output — so every
// failover-eligible failure lands in the safe window and the next member can be
// tried without risk of duplicated output. Mirrors Stream's health-tripping and
// chain-exhaustion behavior.
func (p *Pool) Complete(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	order := p.order(req)
	var lastErr error
	for i, m := range order {
		msg, sr, usage, err := m.Prov.Complete(ctx, req)
		if err == nil {
			p.health.Pass(m.ID)
			return msg, sr, usage, nil
		}
		if !shouldFailover(ctx, err) {
			// Non-failover error (e.g. ctx cancel, deterministic 4xx): surface as-is
			// without tripping health, matching Stream's non-failover path.
			p.health.Pass(m.ID)
			return llm.Message{}, "", llm.Usage{}, err
		}
		lastErr = err
		hard := isHardFailure(err)
		if p.health.Trip(m.ID, trimErr(err), hard) {
			log.Printf("[llmpool] 配置 %q(%s) 已熔断：%s", m.Name, m.Model, trimErr(err))
		}
		if i+1 < len(order) {
			n := order[i+1]
			log.Printf("[llmpool] LLM 故障转移：%q(%s) → %q(%s)，原因：%s",
				m.Name, m.Model, n.Name, n.Model, trimErr(err))
		}
	}
	if lastErr == nil {
		lastErr = ErrExhausted
	}
	log.Printf("[llmpool] 轮询链已耗尽(%d 个配置全部失败)，最后错误：%s", len(order), trimErr(lastErr))
	return llm.Message{}, "", llm.Usage{}, fmt.Errorf("%w：%v", ErrExhausted, lastErr)
}

// order picks the members to try, in order: skip those in a cooling-off window
// and those whose context window can't hold this request, then rotate within each
// equal-priority group so same-priority profiles share the load. Never returns an
// empty slice — if everything is filtered out, the head of the chain is tried
// anyway, since stalling the engine is worse than one more failed request.
func (p *Pool) order(req llm.CompletionRequest) []*Member {
	est := estimateTokens(req)
	var open []*Member
	for _, m := range p.members {
		if p.health.IsOpen(m.ID) {
			continue
		}
		if m.WindowTokens > 0 && est > m.WindowTokens {
			continue // would 400 on length — not a useful failover target
		}
		open = append(open, m)
	}
	if len(open) == 0 {
		return p.members[:1] // last resort: probe the head rather than stall
	}
	return rotateGroups(open, p.rr.Add(1)-1)
}

// rotateGroups rotates each run of equal-ranked members by n, so profiles sharing
// a priority take turns going first (free load-spreading across duplicate keys).
// The chain head has its own Rank and always stays at the front.
func rotateGroups(in []*Member, n uint64) []*Member {
	out := make([]*Member, 0, len(in))
	for i := 0; i < len(in); {
		j := i + 1
		for j < len(in) && in[j].Rank == in[i].Rank {
			j++
		}
		g := in[i:j]
		if len(g) > 1 {
			off := int(n % uint64(len(g)))
			for k := range g {
				out = append(out, g[(k+off)%len(g)])
			}
		} else {
			out = append(out, g...)
		}
		i = j
	}
	return out
}

// statusRe pulls the HTTP status out of the SDK's error text, which is formatted
// as "<prefix>: status <code>: <body>" (norma/llm/retry.go). The SDK exposes no
// typed error, so the string is what we have.
var statusRe = regexp.MustCompile(`status (\d{3})`)

// statusOf returns the HTTP status carried by err, or 0 if it isn't one.
func statusOf(err error) int {
	m := statusRe.FindStringSubmatch(err.Error())
	if m == nil {
		return 0
	}
	code, _ := strconv.Atoi(m[1])
	return code
}

// shouldFailover reports whether err justifies trying the next profile.
//
// Never fails over on context cancellation — that's the user stopping a task or a
// task-level timeout, and burning a backup key on it would both waste credit and
// pollute the run's termination diagnosis. A generic or over-long 400 stays put;
// a vendor-specific parameter reject (see vendorSpecificReject) does fail over.
func shouldFailover(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	switch code := statusOf(err); {
	case code == 0:
		return true // no status → transport-level failure (reset / DNS / timeout)
	case code == 400:
		// A too-long prompt fails the same way on every member. A vendor-specific
		// reject (unknown thinking field, unsupported parameter) does not — the
		// next profile may accept the same request.
		return vendorSpecificReject(err)
	case code == 401, code == 402, code == 403, code == 404, code == 408, code == 429:
		return true
	case code >= 500:
		return true
	}
	return false
}

// isHardFailure reports whether the failure is deterministic (the profile will
// keep failing until a human fixes it) rather than transient. Hard failures open
// the breaker on the first occurrence.
func isHardFailure(err error) bool {
	switch statusOf(err) {
	case 401, 402, 403, 404:
		return true
	}
	return false
}

// trimErr shortens an error for logs/UI — provider bodies can be long.
func trimErr(err error) string {
	s := strings.TrimSpace(strings.ReplaceAll(err.Error(), "\n", " "))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

// overLongRequest matches a 400 whose cause is the prompt itself being too big.
// That failure is identical on every member, so it must not rotate the chain.
var overLongRequest = regexp.MustCompile(`(?i)(context[_\s-]*length|maximum context|context window|prompt is too long|too many tokens|超出.{0,8}上下文|上下文长度)`)

// vendorReject matches a 400 that is about this endpoint's request shape
// (unknown field, unsupported parameter), not about the prompt contents.
var vendorReject = regexp.MustCompile(`(?i)(unsupported[_\s-]*parameter|unknown (?:parameter|field)|unrecognized|unexpected keyword|extra[_\s-]*(?:forbidden|inputs)|enable_thinking|not support|不支持|未知参数|无法识别)`)

// vendorSpecificReject reports whether a 400 is safe to fail over. Over-long
// prompts stay put; parameter/field rejects move to the next profile.
func vendorSpecificReject(err error) bool {
	if err == nil || overLongRequest.MatchString(err.Error()) {
		return false
	}
	return vendorReject.MatchString(err.Error())
}

// estimateTokens roughly sizes a request so members whose context window clearly
// can't hold it are skipped. ASCII is counted at ~3.5 chars/token and other
// characters (CJK included) at ~1.5 chars/token — both slightly high, so a
// near-full Chinese prompt is not skipped on byte length alone. Tool schemas
// are counted from their JSON, not from the number of top-level keys.
func estimateTokens(req llm.CompletionRequest) int {
	n := 0
	for _, s := range req.System {
		n += textTokens(s)
	}
	for _, m := range req.Messages {
		n += blocksTokens(m.Content)
	}
	for _, t := range req.Tools {
		n += textTokens(t.Name) + textTokens(t.Description) + schemaTokens(t.InputSchema)
	}
	return n
}

// textTokens estimates tokens in s. ASCII runes use the historical ~3.5
// chars/token ratio. Every other rune counts as 2/3 of a token (~1.5 chars),
// which matches CJK tokenizers more closely than counting UTF-8 bytes.
func textTokens(s string) int {
	ascii, wide := 0, 0
	for _, r := range s {
		if r < 128 {
			ascii++
		} else {
			wide++
		}
	}
	return ascii*2/7 + wide*2/3
}

func schemaTokens(schema map[string]any) int {
	if len(schema) == 0 {
		return 0
	}
	b, err := json.Marshal(schema)
	if err != nil {
		return 800
	}
	return textTokens(string(b))
}

func blocksTokens(bs []llm.ContentBlock) int {
	n := 0
	for _, b := range bs {
		n += textTokens(b.Text) + textTokens(b.Thinking) + textTokens(string(b.Input)) + textTokens(b.Name)
		if len(b.Content) > 0 {
			n += blocksTokens(b.Content)
		}
	}
	return n
}
