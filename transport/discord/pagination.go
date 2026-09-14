package discord

import "strings"

// Discord counts supplementary characters conservatively as two UTF-16 units.
// Never split a UTF-8 encoding or discard whitespace at a page boundary.
func discordLength(text string) int {
	n := 0
	for _, r := range text {
		n++
		if r > 0xffff {
			n++
		}
	}
	return n
}

type discordPage struct{ text, source string }
type codeFence struct{ marker, opening string }

func fenceLine(line string, active codeFence, max int) (codeFence, bool) {
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 || len(trimmed) < 3 {
		return active, false
	}
	ch := trimmed[0]
	if ch != '`' && ch != '~' {
		return active, false
	}
	n := 0
	for n < len(trimmed) && trimmed[n] == ch {
		n++
	}
	if n < 3 {
		return active, false
	}
	if active.marker != "" {
		if ch == active.marker[0] && n >= len(active.marker) && strings.TrimSpace(trimmed[n:]) == "" && discordLength(line) <= max/4 {
			return codeFence{}, true
		}
		return active, false
	}
	// Exceptionally long delimiter/info lines remain verbatim source text. They
	// cannot be duplicated as wrappers inside Discord's finite page budget.
	if discordLength(line) > max/4 || (ch == '`' && strings.Contains(trimmed[n:], "`")) {
		return active, false
	}
	return codeFence{marker: trimmed[:n], opening: line}, true
}

// paginateDiscord retains source separately from synthetic fence wrappers. That
// makes losslessness explicit: concatenating page.source reproduces the input.
func paginateDiscord(text string, max int) []discordPage {
	if text == "" {
		return nil
	}
	if max < 16 { // No room for fence wrappers; still split safely.
		var pages []discordPage
		var b strings.Builder
		size := 0
		for _, r := range text {
			s := string(r)
			n := discordLength(s)
			if size+n > max && size > 0 {
				v := b.String()
				pages = append(pages, discordPage{v, v})
				b.Reset()
				size = 0
			}
			b.WriteString(s)
			size += n
		}
		if b.Len() > 0 {
			v := b.String()
			pages = append(pages, discordPage{v, v})
		}
		return pages
	}
	var pages []discordPage
	var source strings.Builder
	prefix := ""
	used := 0
	active := codeFence{}
	suffix := func(f codeFence) string {
		if f.marker != "" {
			return "\n" + f.marker
		}
		return ""
	}
	flush := func() {
		raw := source.String()
		pages = append(pages, discordPage{prefix + raw + suffix(active), raw})
		source.Reset()
		prefix = ""
		if active.marker != "" {
			prefix = active.opening + "\n"
		}
		used = discordLength(prefix)
	}
	appendToken := func(token string, next codeFence) {
		n := discordLength(token)
		if used+n+discordLength(suffix(next)) > max && source.Len() > 0 {
			flush()
		}
		source.WriteString(token)
		used += n
		active = next
	}
	for _, line := range strings.SplitAfter(text, "\n") {
		if line == "" {
			continue
		}
		if next, ok := fenceLine(line, active, max); ok {
			appendToken(line, next)
			continue
		}
		for _, r := range line {
			appendToken(string(r), active)
		}
	}
	if source.Len() > 0 {
		flush()
	}
	return pages
}

func discordChunks(text string, max int) []string {
	pages := paginateDiscord(text, max)
	chunks := make([]string, len(pages))
	for i, p := range pages {
		chunks[i] = p.text
	}
	return chunks
}
