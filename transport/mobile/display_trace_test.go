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

func captureBroadcast(t *testing.T, tr *Transport, run func()) []Outbound {
	t.Helper()
	rec := &recorder{}
	tr.subscribe("s1", rec)
	run()
	return rec.frames
}

func tracedOutput(stage sdk.TraceStage, text string, meta map[string]string) sdk.Output {
	return sdk.Output{
		Source:    SourceName,
		SessionID: "s1",
		Trace:     &sdk.TraceEvent{Stage: stage, Text: text},
		Metadata:  meta,
	}
}

func TestDisplayTraceContentBecomesMessageFrame(t *testing.T) {
	tr := newDisplayTransport()
	frames := captureBroadcast(t, tr, func() {
		if err := tr.Display(context.Background(), tracedOutput(sdk.TraceResponseContent, "คำตอบจริง", nil)); err != nil {
			t.Fatalf("Display: %v", err)
		}
	})
	if len(frames) != 1 {
		t.Fatalf("frames = %d, want 1 message frame: %+v", len(frames), frames)
	}
	if frames[0].Kind != FrameMessage || frames[0].Text != "คำตอบจริง" || frames[0].Agent != "main" {
		t.Fatalf("frame = %+v, want main model message", frames[0])
	}
	if got := FinalText(tracedOutput(sdk.TraceResponseContent, "คำตอบจริง", nil)); got != "คำตอบจริง" {
		t.Fatalf("FinalText = %q, want the traced answer", got)
	}
}

func TestDisplayTraceResponseClosesTurn(t *testing.T) {
	tr := newDisplayTransport()
	frames := captureBroadcast(t, tr, func() {
		_ = tr.Display(context.Background(), tracedOutput(sdk.TraceResponseContent, "ตอบแล้ว", nil))
		_ = tr.Display(context.Background(), tracedOutput(sdk.TraceResponse, "", nil))
	})
	if len(frames) != 2 {
		t.Fatalf("frames = %d, want message + done: %+v", len(frames), frames)
	}
	if frames[1].Kind != FrameDone || frames[1].SessionID != "s1" {
		t.Fatalf("terminal frame = %+v, want done for s1", frames[1])
	}
}

func TestDisplayIgnoresStreamDeltasAndOtherSources(t *testing.T) {
	tr := newDisplayTransport()
	frames := captureBroadcast(t, tr, func() {
		_ = tr.Display(context.Background(), tracedOutput(sdk.TraceResponseText, "ทีละคัด", nil))
		_ = tr.Display(context.Background(), sdk.Output{
			Source:    "discord",
			SessionID: "s1",
			Content:   []sdk.ContentPart{{Type: sdk.ContentText, Text: "ไม่ใช่ของเรา"}},
		})
	})
	if len(frames) != 0 {
		t.Fatalf("frames = %d, want none: %+v", len(frames), frames)
	}
}

func TestDisplaySubagentContentIsAttributed(t *testing.T) {
	tr := newDisplayTransport()
	meta := map[string]string{"trace_actor": "subagent", "trace_job_id": "sa-42"}
	frames := captureBroadcast(t, tr, func() {
		_ = tr.Display(context.Background(), tracedOutput(sdk.TraceResponseContent, "ผลจาก sub", meta))
	})
	if len(frames) != 1 || frames[0].Agent != "sub" || frames[0].JobID != "sa-42" {
		t.Fatalf("frames = %+v, want one sub-attributed message", frames)
	}
}

func TestDisplayProgressTraceBecomesStatusFrame(t *testing.T) {
	tr := newDisplayTransport()
	frames := captureBroadcast(t, tr, func() {
		_ = tr.Display(context.Background(), tracedOutput(sdk.TraceToolCall, "read_files", map[string]string{"trace_actor": "subagent"}))
	})
	if len(frames) != 1 || frames[0].Kind != FrameTrace || frames[0].Stage != string(sdk.TraceToolCall) {
		t.Fatalf("frames = %+v, want one trace status frame", frames)
	}
}
