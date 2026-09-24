package mobile

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tulipskun/ai/sdk"
)

type recorder struct{ frames []Outbound }

func (r *recorder) WriteMessage(_ int, data []byte) error {
	var frame Outbound
	if err := json.Unmarshal(data, &frame); err != nil {
		return err
	}
	r.frames = append(r.frames, frame)
	return nil
}

func (r *recorder) Close() error { return nil }

type allowAllVerifier struct{}

func (allowAllVerifier) VerifyToken(context.Context, string) error { return nil }

func newDisplayTransport() *Transport {
	return New(Config{Tokens: &stubTokens{}, Verifier: allowAllVerifier{}})
}

func capture(t *testing.T, tr *Transport, run func()) []Outbound {
	t.Helper()
	rec := &recorder{}
	tr.subscribe("s1", rec)
	run()
	return rec.frames
}

func textTrace(stage sdk.TraceStage, text string) sdk.Output {
	return sdk.Output{
		Source: SourceName, SessionID: "s1",
		Trace: &sdk.TraceEvent{Stage: stage, Text: text},
	}
}

func responseTrace(stage sdk.TraceStage, text string) sdk.Output {
	return sdk.Output{
		Source: SourceName, SessionID: "s1",
		Trace: &sdk.TraceEvent{Stage: stage, Response: &sdk.Response{
			Content: []sdk.ContentPart{{Type: sdk.ContentText, Text: text}},
			Usage:   sdk.Usage{InputTokens: 12, OutputTokens: 34},
		}},
	}
}

func TestStreamedTextBecomesDeltasAndTheTerminalOnlyCloses(t *testing.T) {
	tr := newDisplayTransport()
	frames := capture(t, tr, func() {
		_ = tr.Display(context.Background(), textTrace(sdk.TraceResponseContent, "สวัส"))
		_ = tr.Display(context.Background(), textTrace(sdk.TraceResponseContent, "ดี"))
		_ = tr.Display(context.Background(), responseTrace(sdk.TraceResponse, "สวัสดี"))
	})
	if len(frames) != 3 {
		t.Fatalf("frames = %+v, want two deltas and a done", frames)
	}
	if frames[0].Kind != FrameDelta || frames[0].Text != "สวัส" || frames[0].Role != "model" {
		t.Fatalf("first frame = %+v, want a model delta", frames[0])
	}
	if frames[1].Kind != FrameDelta || frames[1].Text != "ดี" {
		t.Fatalf("second frame = %+v, want the next delta", frames[1])
	}
	if frames[2].Kind != FrameDone {
		t.Fatalf("last frame = %+v, want done without repeating the answer", frames[2])
	}
}

func TestNonStreamedAnswerIsOneMessageThenDone(t *testing.T) {
	tr := newDisplayTransport()
	frames := capture(t, tr, func() {
		_ = tr.Display(context.Background(), responseTrace(sdk.TraceResponseContent, "ตอบเลย"))
		_ = tr.Display(context.Background(), responseTrace(sdk.TraceResponse, "ตอบเลย"))
	})
	if len(frames) != 2 || frames[0].Kind != FrameMessage || frames[1].Kind != FrameDone {
		t.Fatalf("frames = %+v, want message then done", frames)
	}
	if frames[0].Text != "ตอบเลย" || frames[0].InputTokens != 12 || frames[0].OutputTokens != 34 {
		t.Fatalf("message = %+v, want the text and usage", frames[0])
	}
}

func TestToolStagesCarryTheCallAndTheResult(t *testing.T) {
	tr := newDisplayTransport()
	call := &sdk.ToolCall{ID: "call-1", Name: "read_files", Arguments: `{"path":"index.md"}`}
	frames := capture(t, tr, func() {
		_ = tr.Display(context.Background(), sdk.Output{Source: SourceName, SessionID: "s1",
			Trace: &sdk.TraceEvent{Stage: sdk.TraceToolCall, ToolCall: call}})
		_ = tr.Display(context.Background(), sdk.Output{Source: SourceName, SessionID: "s1",
			Trace: &sdk.TraceEvent{Stage: sdk.TraceToolResult, ToolCall: call,
				ToolResult: &sdk.ToolResult{ID: "call-1", Content: "พบ 3 ไฟล์"}}})
	})
	if len(frames) != 2 {
		t.Fatalf("frames = %+v, want two tool frames", frames)
	}
	if frames[0].Stage != string(sdk.TraceToolCall) || frames[0].ToolCall == nil || frames[0].ToolCall.Name != "read_files" {
		t.Fatalf("tool call frame = %+v", frames[0])
	}
	if frames[1].Stage != string(sdk.TraceToolResult) || frames[1].ToolResult == nil ||
		frames[1].ToolResult.Text != "พบ 3 ไฟล์" {
		t.Fatalf("tool result frame = %+v", frames[1])
	}
}

func TestSubagentTextIsAttributedAndDeltasAreMarkedSub(t *testing.T) {
	tr := newDisplayTransport()
	meta := map[string]string{"trace_actor": "subagent", "trace_job_id": "sa-42"}
	frames := capture(t, tr, func() {
		_ = tr.Display(context.Background(), sdk.Output{Source: SourceName, SessionID: "s1", Metadata: meta,
			Trace: &sdk.TraceEvent{Stage: sdk.TraceResponseContent, Text: "กำลังอ่านไฟล์"}})
		_ = tr.Display(context.Background(), sdk.Output{Source: SourceName, SessionID: "s1", Metadata: meta,
			Trace: &sdk.TraceEvent{Stage: sdk.TraceResponse, Response: &sdk.Response{
				Content: []sdk.ContentPart{{Type: sdk.ContentText, Text: "กำลังอ่านไฟล์"}}}}})
	})
	if len(frames) != 2 {
		t.Fatalf("frames = %+v, want a sub delta and a done", frames)
	}
	if frames[0].Agent != "sub" || frames[0].JobID != "sa-42" || frames[0].Kind != FrameDelta {
		t.Fatalf("sub frame = %+v", frames[0])
	}
}

func TestReasoningBecomesAThinkingLineNotAMessage(t *testing.T) {
	tr := newDisplayTransport()
	frames := capture(t, tr, func() {
		_ = tr.Display(context.Background(), textTrace(sdk.TraceResponseText, "กำลังคิด…"))
	})
	if len(frames) != 1 || frames[0].Kind != FrameTrace || frames[0].Stage != string(sdk.TraceResponseText) {
		t.Fatalf("frames = %+v, want one thinking trace", frames)
	}
}

func TestTraceErrorBecomesAnErrorFrame(t *testing.T) {
	tr := newDisplayTransport()
	frames := capture(t, tr, func() {
		_ = tr.Display(context.Background(), sdk.Output{Source: SourceName, SessionID: "s1",
			Trace: &sdk.TraceEvent{Stage: sdk.TraceError, Message: "โควตาไม่พอ"}})
	})
	if len(frames) != 1 || frames[0].Kind != FrameError || frames[0].Text != "โควตาไม่พอ" {
		t.Fatalf("frames = %+v, want the provider error text", frames)
	}
}

func TestFinalTextOnlyCountsTerminalEvents(t *testing.T) {
	if got := FinalText(textTrace(sdk.TraceResponseContent, "ทีละคัด")); got != "" {
		t.Fatalf("FinalText(delta) = %q, want empty so D1 does not grow a turn per chunk", got)
	}
	if got := FinalText(responseTrace(sdk.TraceResponse, "คำตอบเต็ม")); got != "คำตอบเต็ม" {
		t.Fatalf("FinalText(terminal) = %q, want the full answer", got)
	}
	if got := FinalText(sdk.Output{Source: SourceName, SessionID: "s1",
		Content: []sdk.ContentPart{{Type: sdk.ContentText, Text: "ไม่มี trace"}}}); got != "ไม่มี trace" {
		t.Fatalf("FinalText(untraced) = %q", got)
	}
}

func TestOtherSourcesAndEmptyTextAreIgnored(t *testing.T) {
	tr := newDisplayTransport()
	frames := capture(t, tr, func() {
		_ = tr.Display(context.Background(), sdk.Output{Source: "discord", SessionID: "s1",
			Content: []sdk.ContentPart{{Type: sdk.ContentText, Text: "ไม่ใช่ของเรา"}}})
		_ = tr.Display(context.Background(), textTrace(sdk.TraceResponseContent, ""))
	})
	if len(frames) != 0 {
		t.Fatalf("frames = %+v, want none", frames)
	}
}
