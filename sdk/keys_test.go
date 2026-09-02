package sdk

import "testing"

func TestKeyPoolRotates(t *testing.T) {
    p := NewKeyPool("a", "b", "c")
    got, _ := p.Current(); if got != "a" { t.Fatal(got) }
    got, _ = p.Rotate(); if got != "b" { t.Fatal(got) }
    got, _ = p.Rotate(); if got != "c" { t.Fatal(got) }
    got, _ = p.Rotate(); if got != "a" { t.Fatal(got) }
}
