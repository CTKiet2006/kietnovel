package host

import (
	"github.com/CTKiet2006/kietnovel/internal/utils"
	"github.com/voocel/agentcore"
)

// handleSubagentDelta splits a subagent's text from its tool call arguments:
// - DeltaText is emitted directly as markdown
// - DeltaToolCall only has fields extracted and emitted for known long-content tools (e.g. draft_chapter.content); the argument JSON of every other tool is dropped entirely
func (o *observer) handleSubagentDelta(p *agentcore.ProgressPayload) {
	if p.DeltaKind != agentcore.DeltaToolCall {
		o.updateModelState(p.Agent, "Sinh phản hồi")
		o.emitStreamDelta(p.Delta, false)
		return
	}
	if p.Tool == "" {
		return // tool name not ready yet, retry on the next delta
	}
	o.updateModelState(p.Agent, "Sinh "+p.Tool)

	cur, ok := o.streamExtractors[p.Agent]
	// Even after the args of the same tool call have closed (the top-level } was hit), trailing deltas can still arrive:
	// some providers (observed on deepseek-v4-flash) split one set of args into several chunks, and
	// the last chunk may carry whitespace or repeated characters after the `}`. If we handled this with
	// "tool name matches + rebuild on Done", the new extractor would emit another ✻ header and parse the
	// tail tokens as fresh args. These deltas are a redundant tail; just drop them.
	if ok && cur.tool == p.Tool && cur.ext.Done() {
		return
	}
	// The tool name changed, or none exists yet: create a new one.
	if !ok || cur.tool != p.Tool {
		ext := newToolExtractor(p.Tool)
		if ext == nil {
			delete(o.streamExtractors, p.Agent)
			return
		}
		cur = &agentExtractor{tool: p.Tool, ext: ext}
		o.streamExtractors[p.Agent] = cur
	}
	if emitted := cur.ext.Feed(p.Delta); emitted != "" {
		if !cur.emittedAny {
			cur.emittedAny = true
			// streamClear makes the extractor's ✻ header land at the start of the new round, and together with
			// renderStreamContent's HasPrefix("✻") check it takes the renderAgentBlock highlighting
			// path; ensureStreamParagraphBreak only inserts a blank line and does not open a round, so the ✻ is still
			// wrapped by the preceding thinking/body and ends up drawn by renderChapterBlock in the default color.
			o.streamClear()
			// streamClear defensively cleared streamExtractors. The current cur still has to keep feeding the
			// remaining deltas of this tool call, so it must be registered again immediately; otherwise the next
			// deltas would create a new extractor that starts parsing mid-args (it only enters psBeforeKey at the
			// `{` of a nested object) and treats fields such as timeline_events.time / foreshadow_updates.id
			// as top-level ones, making the ✻ header appear twice in the TUI.
			o.streamExtractors[p.Agent] = cur
		}
		o.emitStreamDelta(emitted, false)
	}
}

func (o *observer) emitStreamDelta(delta string, thinking bool) {
	if delta == "" {
		return
	}
	if thinking != o.streamThinking {
		o.emitD(utils.ThinkingSep)
		o.streamThinking = thinking
	}
	o.emitD(delta)
	o.streamHasContent = true
	o.streamLastByte = delta[len(delta)-1]
}

// emitFallbackStreamHeader adds a ✻ title line to the stream panel for tools with no extractor configured.
// Long-content tools emit their header from the extractor along with the argument stream; other tools get
// theirs filled in when ProgressToolStart arrives.
func (o *observer) emitFallbackStreamHeader(tool string) {
	if _, has := toolDisplays[tool]; has {
		return // an extractor exists, so the extractor emits the header itself
	}
	o.streamClear()
	o.emitStreamDelta(streamHeaderFallback(tool)+"\n", false)
}

// streamHeaderFallback builds the streaming header text for tools with no extractor configured,
// so the user sees "what is being called" even for lightweight read-only tools.
//
// The "✻ " prefix is the conventional "agent dispatch block" marker - the TUI's renderStreamContent renders
// this prefix through the renderAgentBlock path (icon + highlighted label + divider),
// otherwise it falls to the body-block path with the terminal default color and the header just looks like ordinary body text.
func streamHeaderFallback(tool string) string {
	return "✻ " + tool
}

// streamClear tells the TUI to open a new streamRound, and also resets the paragraph-separation state.
// Logically a new round is an "empty stream", otherwise the next extractor's first emit would wrongly insert a leading blank line.
//
// streamThinking must be reset as well: emitStreamDelta uses streamThinking across calls to track
// whether the previous segment was thinking. Nothing has been emitted yet in the new round, so the next
// emit(thinking=false) should not insert a ThinkingSep. Otherwise a fallback header (e.g. ✻ reading a chapter)
// would be grabbed first by \x02, renderStreamContent's HasPrefix("✻") would miss, the whole segment would
// fall to the body path and then be split by ThinkingSep into a thinking segment, painting the title in the thinking color.
func (o *observer) streamClear() {
	o.emitC()
	o.streamHasContent = false
	o.streamLastByte = 0
	o.streamThinking = false
	// ProgressToolEnd already deleted this before the previous round's subagent finished; cleared defensively here.
	if len(o.streamExtractors) > 0 {
		o.streamExtractors = make(map[string]*agentExtractor)
	}
}
