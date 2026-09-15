package tools

import (
	"bytes"
	"compress/flate"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// This file holds the standard-library-only analysis behind describe_attachment
// and read_attachment. There is no PDF or image-metadata dependency in this
// module and adding one is not allowed, so the PDF path is deliberately
// best-effort and says so in its notes:
//
//   - page counting reads /Type /Page objects, falling back to the /Count of a
//     /Pages tree, and reports that nothing was found when neither is visible;
//     the count is a keyword tally, so an unusual file that embeds page
//     dictionaries inside a /Pages array without their own objects can be
//     over-counted;
//   - text comes only from uncompressed or FlateDecode (zlib, with a raw
//     deflate fallback) content streams; streams that declare another filter
//     (DCTDecode, JPXDecode, CCITTFaxDecode, LZWDecode, RunLengthDecode,
//     ASCIIHexDecode, ...) are skipped and named in notes;
//   - object streams (/ObjStm) and cross-reference streams are not expanded, so
//     pages or text that live only inside them are invisible;
//   - font encodings are not consulted, so subset or CID fonts can come out as
//     mojibake; a hex string is read as UTF-16BE when it plausibly is one.
//
// Image summarising uses image.DecodeConfig, which reads only the header for
// PNG, JPEG, and GIF; other raster formats have no standard-library header
// decoder and are reported as metadata only.

const (
	maxPDFStreams     = 128
	maxPDFStreamBytes = int64(1 << 20)
)

var (
	pdfPageObjectRE = regexp.MustCompile(`(?s)/Type\s*/Page\b`)
	pdfPagesCountRE = regexp.MustCompile(`(?s)/Type\s*/Pages\b.*?/Count\s+(\d+)`)
	pdfCountRE      = regexp.MustCompile(`(?s)/Count\s+(\d+)`)
	pdfImageRE      = regexp.MustCompile(`(?s)/Subtype\s*/Image\b`)
	pdfTjRE         = regexp.MustCompile(`\bTj\b`)
	pdfTJRE         = regexp.MustCompile(`\bTJ\b`)
	pdfFilterRE     = regexp.MustCompile(`/(FlateDecode|DCTDecode|JPXDecode|CCITTFaxDecode|LZWDecode|RunLengthDecode|ASCIIHexDecode|ASCII85Decode|JBIG2Decode|ObjStm|XRef)\b`)
)

// pdfSummary is the analysis describe_attachment publishes for a PDF.
type pdfSummary struct {
	pages           int
	pagesDiscovered bool
	images          int
	textOperators   int
	streamsRead     int
	streamsSkipped  int
	text            string
	notes           []string
}

// summarizePDF scans a PDF buffer and returns metadata plus whatever text the
// standard library can reach.
func summarizePDF(data []byte) pdfSummary {
	summary := pdfSummary{}
	if pages := len(pdfPageObjectRE.FindAll(data, -1)); pages > 0 {
		summary.pages = pages
		summary.pagesDiscovered = true
	} else if match := pdfPagesCountRE.FindSubmatch(data); len(match) == 2 {
		if value, err := strconv.Atoi(string(match[1])); err == nil && value > 0 {
			summary.pages = value
			summary.pagesDiscovered = true
		}
	} else if match := pdfCountRE.FindSubmatch(data); len(match) == 2 {
		if value, err := strconv.Atoi(string(match[1])); err == nil && value > 0 {
			summary.pages = value
			summary.pagesDiscovered = true
			summary.notes = append(summary.notes, "page count taken from a /Count entry, not from visible /Page objects")
		}
	}
	if !summary.pagesDiscovered {
		summary.notes = append(summary.notes, "no /Page object or /Pages /Count was found, so the page count is unknown; it may live in an object stream")
	}
	summary.images = len(pdfImageRE.FindAll(data, -1))

	var text strings.Builder
	named := map[string]bool{}
	streamCount := 0
	for _, stream := range pdfStreams(data) {
		if streamCount >= maxPDFStreams {
			summary.notes = append(summary.notes, fmt.Sprintf("stopped after %d content streams", maxPDFStreams))
			break
		}
		streamCount++
		filter := pdfStreamFilter(stream.dict)
		payload, err := decodePDFStream(stream.payload, filter)
		if err != nil {
			summary.streamsSkipped++
			if filter == "" {
				filter = "unfiltered"
			}
			if !named[filter] {
				named[filter] = true
				summary.notes = append(summary.notes, fmt.Sprintf("stream %d (%s) was skipped: %v", streamCount, filter, err))
			}
			continue
		}
		summary.streamsRead++
		extracted, operators := pdfContentText(payload)
		if operators == 0 {
			continue
		}
		summary.textOperators += operators
		text.WriteString(extracted)
		text.WriteByte('\n')
	}
	summary.text = collapseBlankLines(strings.ToValidUTF8(text.String(), "\uFFFD"))
	if summary.textOperators == 0 {
		summary.notes = append(summary.notes, "no readable Tj/TJ text operators were reached; the PDF may be image-only or use a filter the standard library cannot decode")
	}
	if summary.streamsSkipped > 0 {
		summary.notes = append(summary.notes, fmt.Sprintf("%d stream(s) were skipped as undecodable", summary.streamsSkipped))
	}
	return summary
}

// pdfStream is one raw content stream plus the object dictionary that describes
// it, which is where the /Filter lives.
type pdfStream struct {
	dict    []byte
	payload []byte
}

// pdfStreams walks the file for stream/endstream pairs.
func pdfStreams(data []byte) []pdfStream {
	var out []pdfStream
	position := 0
	for position < len(data) {
		index := indexPDFStreamKeyword(data, position)
		if index < 0 {
			break
		}
		dictStart := bytes.LastIndex(data[:index], []byte("obj"))
		payloadStart := index + len("stream")
		if payloadStart < len(data) && data[payloadStart] == '\r' {
			payloadStart++
		}
		if payloadStart < len(data) && data[payloadStart] == '\n' {
			payloadStart++
		}
		end := bytes.Index(data[payloadStart:], []byte("endstream"))
		if end < 0 {
			break
		}
		if dictStart < 0 {
			dictStart = 0
		}
		out = append(out, pdfStream{dict: data[dictStart:index], payload: data[payloadStart : payloadStart+end]})
		position = payloadStart + end + len("endstream")
	}
	return out
}

// indexPDFStreamKeyword finds the next "stream" keyword that is not part of
// "endstream" or a name, so paired streams are not mistaken for their own
// terminator.
func indexPDFStreamKeyword(data []byte, from int) int {
	position := from
	for {
		index := bytes.Index(data[position:], []byte("stream"))
		if index < 0 {
			return -1
		}
		index += position
		if index >= 3 && string(data[index-3:index]) == "end" {
			position = index + len("stream")
			continue
		}
		if index > 0 && isPDFNameByte(data[index-1]) {
			position = index + len("stream")
			continue
		}
		return index
	}
}

func isPDFNameByte(value byte) bool {
	switch {
	case value >= 'a' && value <= 'z', value >= 'A' && value <= 'Z', value >= '0' && value <= '9':
		return true
	case value == '.', value == '+', value == '-':
		return true
	default:
		return false
	}
}

// pdfStreamFilter names the /Filter keyword of a stream dictionary, if any.
func pdfStreamFilter(dict []byte) string {
	match := pdfFilterRE.FindSubmatch(dict)
	if len(match) == 0 {
		return ""
	}
	return string(match[1])
}

// decodePDFStream returns the bytes of a stream body that this package can
// actually interpret. Anything that needs a non-Flate decoder is an error so
// the caller can record which filter was unsupported.
func decodePDFStream(payload []byte, filter string) ([]byte, error) {
	switch filter {
	case "FlateDecode":
		return inflateFlate(payload)
	case "":
		if utf8.Valid(payload) {
			return payload, nil
		}
		if inflated, err := inflateFlate(payload); err == nil {
			return inflated, nil
		}
		return nil, errors.New("stream is neither readable text nor a zlib/deflate stream")
	default:
		return nil, fmt.Errorf("%s needs a decoder that is not in the standard library", filter)
	}
}

// inflateFlate accepts a zlib stream and falls back to raw deflate, because PDF
// writers emit both.
func inflateFlate(payload []byte) ([]byte, error) {
	reader, err := zlib.NewReader(bytes.NewReader(payload))
	if err != nil {
		raw := flate.NewReader(bytes.NewReader(payload))
		data, rawErr := io.ReadAll(io.LimitReader(raw, maxPDFStreamBytes))
		_ = raw.Close()
		if rawErr != nil {
			return nil, errors.New("not a zlib or raw deflate stream")
		}
		return data, nil
	}
	data, readErr := io.ReadAll(io.LimitReader(reader, maxPDFStreamBytes))
	closeErr := reader.Close()
	if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		return nil, readErr
	}
	if closeErr != nil && !errors.Is(closeErr, io.ErrUnexpectedEOF) && !errors.Is(closeErr, io.EOF) {
		return nil, closeErr
	}
	return data, nil
}

// pdfContentText pulls the strings shown by the text operators of one decoded
// content stream. It is token based, so a minified stream that drops every
// space between an operand and its operator still works; operator keywords
// themselves stay separated, as the PDF grammar requires (the tokenizer reads a
// run of letters as one operator and ignores it if that run is not a text
// operator). It returns the text and how many text-showing operators were
// consumed.
func pdfContentText(content []byte) (string, int) {
	if len(pdfTjRE.FindAll(content, 1)) == 0 && len(pdfTJRE.FindAll(content, 1)) == 0 {
		return "", 0
	}
	var (
		builder   strings.Builder
		operators int
		index     int
	)
	for index < len(content) {
		char := content[index]
		switch {
		case char == '(':
			literal, next := readPDFLiteral(content, index)
			operator, operatorEnd := peekPDFOperator(content, next)
			if operator == "Tj" || operator == "'" || operator == "\"" {
				builder.WriteString(decodePDFStringBytes(literal))
				operators++
				if operator != "Tj" {
					builder.WriteByte('\n')
				}
				index = operatorEnd
				continue
			}
			index = next
		case char == '<' && index+1 < len(content) && content[index+1] != '<':
			raw, next := readPDFHexadecimal(content, index)
			operator, operatorEnd := peekPDFOperator(content, next)
			if operator == "Tj" {
				builder.WriteString(decodePDFHexString(raw))
				operators++
				index = operatorEnd
				continue
			}
			index = next
		case char == '[':
			elements, next := readPDFArray(content, index)
			operator, operatorEnd := peekPDFOperator(content, next)
			if operator == "TJ" {
				for _, element := range elements {
					builder.WriteString(decodePDFStringBytes(element))
				}
				operators++
				index = operatorEnd
				continue
			}
			index = next
		case char == 'T' && index+1 < len(content) && content[index+1] == '*':
			builder.WriteByte('\n')
			index += 2
		case char == 'B' && index+1 < len(content) && content[index+1] == 'T':
			index += 2
		case char == 'E' && index+1 < len(content) && content[index+1] == 'T':
			builder.WriteByte('\n')
			index += 2
		default:
			index++
		}
	}
	return builder.String(), operators
}

// readPDFLiteral returns the bytes inside a (...) string, honouring PDF escape
// sequences and nested parentheses. index points at the opening parenthesis.
func readPDFLiteral(content []byte, index int) ([]byte, int) {
	depth := 0
	var body []byte
	for position := index; position < len(content); position++ {
		char := content[position]
		switch char {
		case '\\':
			if position+1 >= len(content) {
				return body, len(content)
			}
			position++
			if decoded := unescapePDFByte(content, &position); decoded != 0 {
				body = append(body, decoded)
			}
		case '(':
			depth++
			if depth == 1 {
				continue
			}
			body = append(body, char)
		case ')':
			depth--
			if depth <= 0 {
				return body, position + 1
			}
			body = append(body, char)
		default:
			body = append(body, char)
		}
	}
	return body, len(content)
}

// unescapePDFByte resolves one escape sequence; content[position] is the byte
// after the backslash and is advanced through octal digits.
func unescapePDFByte(content []byte, position *int) byte {
	char := content[*position]
	switch char {
	case 'n':
		return '\n'
	case 'r':
		return '\r'
	case 't':
		return '\t'
	case 'b':
		return '\b'
	case 'f':
		return '\f'
	case '\n', '\r':
		// A backslash before an end-of-line is a line continuation.
		for *position+1 < len(content) && (content[*position+1] == '\r' || content[*position+1] == '\n') {
			*position++
		}
		return 0
	case '0', '1', '2', '3', '4', '5', '6', '7':
		value := 0
		digits := 0
		for *position < len(content) && digits < 3 && content[*position] >= '0' && content[*position] <= '7' {
			value = value*8 + int(content[*position]-'0')
			*position++
			digits++
		}
		*position--
		return byte(value & 0xff)
	default:
		return char
	}
}

// readPDFHexadecimal returns the bytes inside a <...> string. index points at
// the opening angle bracket, which the caller checked is not a dictionary
// marker.
func readPDFHexadecimal(content []byte, index int) ([]byte, int) {
	end := bytes.IndexByte(content[index+1:], '>')
	if end < 0 {
		return nil, len(content)
	}
	raw, err := hexBytes(content[index+1 : index+1+end])
	if err != nil {
		return nil, index + 1 + end + 1
	}
	return raw, index + 1 + end + 1
}

// readPDFArray returns the string operands of a [...] TJ array.
func readPDFArray(content []byte, index int) ([][]byte, int) {
	var elements [][]byte
	position := index + 1
	for position < len(content) {
		switch content[position] {
		case ']':
			return elements, position + 1
		case '(':
			literal, next := readPDFLiteral(content, position)
			elements = append(elements, literal)
			position = next
		case '<':
			if position+1 < len(content) && content[position+1] == '<' {
				position += 2
				continue
			}
			raw, next := readPDFHexadecimal(content, position)
			elements = append(elements, raw)
			position = next
		default:
			position++
		}
	}
	return elements, len(content)
}

// peekPDFOperator returns the operator keyword that follows a string operand.
func peekPDFOperator(content []byte, index int) (string, int) {
	position := index
	for position < len(content) && isPDFWhitespace(content[position]) {
		position++
	}
	start := position
	for position < len(content) && isPDFOperatorByte(content[position]) {
		position++
	}
	if start == position {
		return "", index
	}
	return string(content[start:position]), position
}

func isPDFWhitespace(value byte) bool {
	switch value {
	case ' ', '\t', '\r', '\n', '\f', 0:
		return true
	default:
		return false
	}
}

func isPDFOperatorByte(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

func decodePDFStringBytes(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	if utf8.Valid(body) {
		return string(body)
	}
	return strings.ToValidUTF8(string(body), "\uFFFD")
}

// decodePDFHexString handles the two encodings a PDF text string can carry:
// single-byte codes for simple fonts, and UTF-16BE behind a byte-order mark or
// in a CID-style encoding.
func decodePDFHexString(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	if len(raw) >= 2 && raw[0] == 0xFE && raw[1] == 0xFF {
		return decodeUTF16BE(raw[2:])
	}
	if len(raw)%2 == 0 && looksLikeUTF16BE(raw) {
		return decodeUTF16BE(raw)
	}
	return decodePDFStringBytes(raw)
}

func decodeUTF16BE(raw []byte) string {
	units := make([]uint16, 0, len(raw)/2)
	for index := 0; index+1 < len(raw); index += 2 {
		units = append(units, binary.BigEndian.Uint16(raw[index:index+2]))
	}
	return string(utf16.Decode(units))
}

func looksLikeUTF16BE(raw []byte) bool {
	if len(raw) < 4 || len(raw)%2 != 0 {
		return false
	}
	for index := 0; index < len(raw); index += 2 {
		if raw[index] != 0 {
			return false
		}
	}
	return true
}

func hexBytes(body []byte) ([]byte, error) {
	digits := make([]byte, 0, len(body))
	for _, char := range body {
		if isPDFWhitespace(char) {
			continue
		}
		if char >= '0' && char <= '9' || char >= 'A' && char <= 'F' || char >= 'a' && char <= 'f' {
			digits = append(digits, char)
			continue
		}
		return nil, fmt.Errorf("invalid hex digit %q", char)
	}
	if len(digits)%2 == 1 {
		digits = append(digits, '0')
	}
	out := make([]byte, len(digits)/2)
	for index := range out {
		value, err := strconv.ParseUint(string(digits[index*2:index*2+2]), 16, 8)
		if err != nil {
			return nil, err
		}
		out[index] = byte(value)
	}
	return out, nil
}

// collapseBlankLines squeezes runs of blank lines so a document stays a compact
// summary.
func collapseBlankLines(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			blank = true
			continue
		}
		if blank && len(out) > 0 {
			out = append(out, "")
		}
		blank = false
		out = append(out, strings.TrimRight(line, " \t\r"))
	}
	return strings.Join(out, "\n")
}
