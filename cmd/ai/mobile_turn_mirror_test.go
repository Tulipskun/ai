package main

import "testing"

// A streamed answer is reported by the sdk as a content event and again on the
// terminal one: D1 must still end up with a single row for the turn.
func TestTurnMirrorAdmitsOneRowPerTurn(t *testing.T) {
	var mirror turnMirror
	key := "work-1\x00คำตอบเดียว"
	if !mirror.take(key, false) {
		t.Fatal("the first report of a turn must be admitted")
	}
	if mirror.take(key, true) {
		t.Fatal("the terminal repeat must be dropped")
	}
	if !mirror.take(key, false) {
		t.Fatal("a later turn with the same text must be admitted again")
	}
}

func TestTurnMirrorForgetsAFailedWrite(t *testing.T) {
	var mirror turnMirror
	key := "work-2\x00ok"
	if !mirror.take(key, false) {
		t.Fatal("first report must be admitted")
	}
	mirror.forget(key)
	if !mirror.take(key, false) {
		t.Fatal("a failed write must not block the retry")
	}
}
