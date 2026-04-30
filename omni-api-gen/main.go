package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
)

// protoFiles lists every .proto file to include, in display order.
var protoFiles = []string{
	"management/management.proto",
	"oidc/oidc.proto",
	"resources/resources.proto",
	"specs/auth.proto",
	"specs/ephemeral.proto",
	"specs/infra.proto",
	"specs/oidc.proto",
	"specs/omni.proto",
	"specs/siderolink.proto",
	"specs/system.proto",
	"specs/virtual.proto",
}

const (
	baseURL   = "https://raw.githubusercontent.com/siderolabs/omni/main/client/api/omni/"
	frontmatter = `---
title: Omni API
description: Omni gRPC API reference.
---

`
	scalarTable = `## Scalar Value Types

| .proto Type | Notes | C++ | Java | Python | Go | C# | PHP | Ruby |
| ----------- | ----- | --- | ---- | ------ | -- | -- | --- | ---- |
| <a name="double" /> double |  | double | double | float | float64 | double | float | Float |
| <a name="float" /> float |  | float | float | float | float32 | float | float | Float |
| <a name="int32" /> int32 | Uses variable-length encoding. Inefficient for encoding negative numbers – if your field is likely to have negative values, use sint32 instead. | int32 | int | int | int32 | int | integer | Bignum or Fixnum (as required) |
| <a name="int64" /> int64 | Uses variable-length encoding. Inefficient for encoding negative numbers – if your field is likely to have negative values, use sint64 instead. | int64 | long | int/long | int64 | long | integer/string | Bignum |
| <a name="uint32" /> uint32 | Uses variable-length encoding. | uint32 | int | int/long | uint32 | uint | integer | Bignum or Fixnum (as required) |
| <a name="uint64" /> uint64 | Uses variable-length encoding. | uint64 | long | int/long | uint64 | ulong | integer/string | Bignum or Fixnum (as required) |
| <a name="sint32" /> sint32 | Uses variable-length encoding. Signed int value. These more efficiently encode negative numbers than regular int32s. | int32 | int | int | int32 | int | integer | Bignum or Fixnum (as required) |
| <a name="sint64" /> sint64 | Uses variable-length encoding. Signed int value. These more efficiently encode negative numbers than regular int64s. | int64 | long | int/long | int64 | long | integer/string | Bignum |
| <a name="fixed32" /> fixed32 | Always four bytes. More efficient than uint32 if values are often greater than 2^28. | uint32 | int | int | uint32 | uint | integer | Bignum or Fixnum (as required) |
| <a name="fixed64" /> fixed64 | Always eight bytes. More efficient than uint64 if values are often greater than 2^56. | uint64 | long | int/long | uint64 | ulong | integer/string | Bignum |
| <a name="sfixed32" /> sfixed32 | Always four bytes. | int32 | int | int | int32 | int | integer | Bignum or Fixnum (as required) |
| <a name="sfixed64" /> sfixed64 | Always eight bytes. | int64 | long | int/long | int64 | long | integer/string | Bignum |
| <a name="bool" /> bool |  | bool | boolean | boolean | bool | bool | boolean | TrueClass/FalseClass |
| <a name="string" /> string | A string must always contain UTF-8 encoded or 7-bit ASCII text. | string | String | str/unicode | string | string | string | String (UTF-8) |
| <a name="bytes" /> bytes | May contain any arbitrary sequence of bytes. | string | ByteString | str | []byte | ByteString | string | String (ASCII-8BIT) |
`
)

var scalars = map[string]bool{
	"double": true, "float": true, "int32": true, "int64": true,
	"uint32": true, "uint64": true, "sint32": true, "sint64": true,
	"fixed32": true, "fixed64": true, "sfixed32": true, "sfixed64": true,
	"bool": true, "string": true, "bytes": true,
}

// ---------- data model ----------

type ProtoFile struct {
	Path     string
	Package  string
	Messages []*Message
	Enums    []*Enum
	Services []*Service
}

type Message struct {
	Name     string // simple name
	FullName string // pkg.Outer.Inner
	Comment  string
	Fields   []*Field
	Nested   []*Message
	Enums    []*Enum
}

type Field struct {
	Name    string
	Type    string
	Label   string
	Comment string
}

type Enum struct {
	Name     string
	FullName string
	Comment  string
	Values   []*EnumValue
}

type EnumValue struct {
	Name    string
	Number  string
	Comment string
}

type Service struct {
	Name    string
	Comment string
	Methods []*Method
}

type Method struct {
	Name         string
	Input        string
	Output       string
	ServerStream bool
	Comment      string
}

// ---------- fetcher ----------

func fetch(url string) (string, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "omni-api-gen")
	token := os.Getenv("GITHUB_TOKEN")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}
	return string(body), nil
}

// ---------- parser ----------

var (
	rePkg     = regexp.MustCompile(`^package\s+([\w.]+)\s*;`)
	reMsg     = regexp.MustCompile(`^message\s+(\w+)\s*\{`)
	reEnum    = regexp.MustCompile(`^enum\s+(\w+)\s*\{`)
	reSvc     = regexp.MustCompile(`^service\s+(\w+)\s*\{`)
	reField   = regexp.MustCompile(`^(repeated\s+)?(\S+)\s+(\w+)\s*=\s*\d+`)
	reMap     = regexp.MustCompile(`^map<([^>]+)>\s+(\w+)\s*=\s*\d+`)
	reOneof   = regexp.MustCompile(`^oneof\s+\w+\s*\{`)
	reRPC     = regexp.MustCompile(`^rpc\s+(\w+)\s*\(([^)]+)\)\s+returns\s+\(([^)]+)\)`)
	reEnumVal = regexp.MustCompile(`^(\w+)\s*=\s*(-?\d+)\s*;`)
	reComment = regexp.MustCompile(`^//\s?(.*)`)
)

type parseState int

const (
	stateTop parseState = iota
	stateMessage
	stateEnum
	stateService
	stateOneof
)

type stackFrame struct {
	state   parseState
	message *Message // non-nil when stateMessage
	enum    *Enum    // non-nil when stateEnum
	service *Service // non-nil when stateService
}

func parseProto(path, content string) *ProtoFile {
	f := &ProtoFile{Path: path}

	lines := strings.Split(content, "\n")

	var stack []stackFrame
	stack = append(stack, stackFrame{state: stateTop})

	var pendingComment strings.Builder

	collectComment := func() string {
		s := strings.TrimSpace(pendingComment.String())
		pendingComment.Reset()
		return s
	}

	top := func() *stackFrame { return &stack[len(stack)-1] }

	push := func(frame stackFrame) { stack = append(stack, frame) }

	pop := func() {
		if len(stack) <= 1 {
			return
		}
		frame := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		parent := top()

		switch frame.state {
		case stateMessage:
			msg := frame.message
			switch parent.state {
			case stateTop:
				f.Messages = append(f.Messages, msg)
			case stateMessage:
				parent.message.Nested = append(parent.message.Nested, msg)
			}
		case stateEnum:
			e := frame.enum
			switch parent.state {
			case stateTop:
				f.Enums = append(f.Enums, e)
			case stateMessage:
				parent.message.Enums = append(parent.message.Enums, e)
			}
		case stateService:
			f.Services = append(f.Services, frame.service)
		}
	}

	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)

		// Strip trailing inline comment from code lines (but keep pure comment lines)
		// and strip inline options like [deprecated = true]
		codeForParsing := stripInlineComment(line)
		codeForParsing = stripFieldOptions(codeForParsing)

		// Pure comment line
		if m := reComment.FindStringSubmatch(line); m != nil {
			pendingComment.WriteString(m[1] + "\n")
			continue
		}

		// Blank line resets pending comment only at top level
		if line == "" {
			if top().state == stateTop {
				pendingComment.Reset()
			}
			continue
		}

		// Skip syntax, option, import, reserved lines
		if strings.HasPrefix(line, "syntax ") ||
			strings.HasPrefix(line, "option ") ||
			strings.HasPrefix(line, "import ") ||
			strings.HasPrefix(line, "reserved ") ||
			line == "}" || line == "};" {
			if line == "}" || line == "};" {
				if top().state == stateOneof {
					pop()
				} else {
					pop()
				}
				pendingComment.Reset()
			}
			if strings.HasPrefix(line, "reserved ") {
				pendingComment.Reset()
			}
			continue
		}

		// Package
		if m := rePkg.FindStringSubmatch(codeForParsing); m != nil {
			f.Package = m[1]
			pendingComment.Reset()
			continue
		}

		// Message
		if m := reMsg.FindStringSubmatch(codeForParsing); m != nil {
			comment := collectComment()
			msg := &Message{Name: m[1], Comment: comment}
			msg.FullName = buildFullName(f.Package, stack, m[1])
			push(stackFrame{state: stateMessage, message: msg})
			continue
		}

		// Enum
		if m := reEnum.FindStringSubmatch(codeForParsing); m != nil {
			comment := collectComment()
			e := &Enum{Name: m[1], Comment: comment}
			e.FullName = buildFullName(f.Package, stack, m[1])
			push(stackFrame{state: stateEnum, enum: e})
			continue
		}

		// Service
		if m := reSvc.FindStringSubmatch(codeForParsing); m != nil {
			comment := collectComment()
			svc := &Service{Name: m[1], Comment: comment}
			push(stackFrame{state: stateService, service: svc})
			continue
		}

		// oneof
		if reOneof.MatchString(codeForParsing) {
			pendingComment.Reset()
			push(stackFrame{state: stateOneof})
			continue
		}

		// Closing brace
		if codeForParsing == "}" || codeForParsing == "};" {
			pop()
			pendingComment.Reset()
			continue
		}

		switch top().state {
		case stateMessage, stateOneof:
			msg := top().message
			if top().state == stateOneof {
				// find the parent message
				for i := len(stack) - 1; i >= 0; i-- {
					if stack[i].state == stateMessage {
						msg = stack[i].message
						break
					}
				}
			}
			if msg == nil {
				pendingComment.Reset()
				continue
			}
			comment := collectComment()

			// map field
			if mm := reMap.FindStringSubmatch(codeForParsing); mm != nil {
				msg.Fields = append(msg.Fields, &Field{
					Name:    mm[2],
					Type:    "map<" + mm[1] + ">",
					Label:   "map",
					Comment: comment,
				})
				continue
			}

			// repeated or regular field
			if mm := reField.FindStringSubmatch(codeForParsing); mm != nil {
				label := ""
				if strings.TrimSpace(mm[1]) == "repeated" {
					label = "repeated"
				}
				if top().state == stateOneof {
					label = "oneof"
				}
				msg.Fields = append(msg.Fields, &Field{
					Name:    mm[3],
					Type:    mm[2],
					Label:   label,
					Comment: comment,
				})
				continue
			}
			pendingComment.Reset()

		case stateEnum:
			e := top().enum
			comment := collectComment()
			if mm := reEnumVal.FindStringSubmatch(codeForParsing); mm != nil {
				e.Values = append(e.Values, &EnumValue{
					Name:    mm[1],
					Number:  mm[2],
					Comment: comment,
				})
			}

		case stateService:
			svc := top().service
			comment := collectComment()
			if mm := reRPC.FindStringSubmatch(codeForParsing); mm != nil {
				output := strings.TrimSpace(mm[3])
				stream := false
				if strings.HasPrefix(output, "stream ") {
					stream = true
					output = strings.TrimPrefix(output, "stream ")
				}
				svc.Methods = append(svc.Methods, &Method{
					Name:         mm[1],
					Input:        strings.TrimSpace(mm[2]),
					Output:       strings.TrimSpace(output),
					ServerStream: stream,
					Comment:      comment,
				})
			}

		default:
			pendingComment.Reset()
		}
	}

	return f
}

func buildFullName(pkg string, stack []stackFrame, name string) string {
	parts := []string{pkg}
	for _, f := range stack[1:] { // skip top-level frame
		if f.state == stateMessage && f.message != nil {
			parts = append(parts, f.message.Name)
		}
	}
	parts = append(parts, name)
	return strings.Join(parts, ".")
}

// escapeDesc flattens a multi-line comment into a single line and fixes
// known MDX-unsafe strings.
func escapeDesc(comment string) string {
	s := strings.ReplaceAll(comment, "\n", " ")
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "<year>-<month>-<day>", "`<year>-<month>-<day>`")
	return s
}

func stripInlineComment(line string) string {
	idx := strings.Index(line, " //")
	if idx >= 0 {
		return strings.TrimSpace(line[:idx])
	}
	return line
}

func stripFieldOptions(line string) string {
	// Remove [deprecated = true] style options
	re := regexp.MustCompile(`\s*\[[^\]]+\]`)
	return re.ReplaceAllString(line, "")
}

// ---------- MDX generator ----------

func formatType(t string, pkg string) string {
	if t == "" {
		return ""
	}

	// map<K, V> — show label in the Label column, type is the inner types
	if strings.HasPrefix(t, "map<") {
		inner := strings.TrimPrefix(t, "map<")
		inner = strings.TrimSuffix(inner, ">")
		parts := strings.SplitN(inner, ",", 2)
		if len(parts) == 2 {
			k := strings.TrimSpace(parts[0])
			v := strings.TrimSpace(parts[1])
			return fmt.Sprintf("%s, %s", fmtSingleType(k, pkg), fmtSingleType(v, pkg))
		}
		return inner
	}

	return fmtSingleType(t, pkg)
}

func fmtSingleType(t, pkg string) string {
	t = strings.TrimSpace(t)
	if scalars[t] {
		return fmt.Sprintf("[%s](#%s)", t, t)
	}
	if t == "google.protobuf.Empty" {
		return "[google.protobuf.Empty](#google.protobuf.Empty)"
	}
	if strings.HasPrefix(t, "google.protobuf.") {
		return fmt.Sprintf("[%s](#%s)", t, t)
	}
	if strings.HasPrefix(t, "google.rpc.") {
		return fmt.Sprintf("[%s](#%s)", t, t)
	}
	// type from another package (contains dot already)
	if strings.Contains(t, ".") {
		return fmt.Sprintf("[%s](#%s)", t, t)
	}
	// same package
	return fmt.Sprintf("[%s](#%s.%s)", t, pkg, t)
}

// allMessages returns all messages in a file, flattened (nested included).
func allMessages(msgs []*Message) []*Message {
	var result []*Message
	for _, m := range msgs {
		result = append(result, m)
		result = append(result, allMessages(m.Nested)...)
	}
	return result
}

// allEnums returns top-level and nested enums.
func allEnumsInMsg(m *Message) []*Enum {
	var result []*Enum
	result = append(result, m.Enums...)
	for _, nested := range m.Nested {
		result = append(result, allEnumsInMsg(nested)...)
	}
	return result
}

func allEnums(f *ProtoFile) []*Enum {
	var result []*Enum
	result = append(result, f.Enums...)
	for _, m := range f.Messages {
		result = append(result, allEnumsInMsg(m)...)
	}
	return result
}

func generateTOC(files []*ProtoFile) string {
	var sb strings.Builder
	sb.WriteString("## Table of Contents\n\n")

	for _, f := range files {
		anchor := strings.ReplaceAll(f.Path, "/", "/")
		sb.WriteString(fmt.Sprintf("- [%s](#%s)\n", f.Path, anchor))

		msgs := allMessages(f.Messages)
		for _, m := range msgs {
			sb.WriteString(fmt.Sprintf("    - [%s](#%s)\n", m.Name, m.FullName))
		}

		enums := allEnums(f)
		if len(enums) > 0 {
			sb.WriteString("  \n")
			for _, e := range enums {
				sb.WriteString(fmt.Sprintf("    - [%s](#%s)\n", e.Name, e.FullName))
			}
		}

		if len(f.Services) > 0 {
			sb.WriteString("  \n")
			for _, svc := range f.Services {
				sb.WriteString(fmt.Sprintf("    - [%s](#%s.%s)\n", svc.Name, f.Package, svc.Name))
			}
		}

		sb.WriteString("  \n")
	}

	sb.WriteString("- [Scalar Value Types](#scalar-value-types)\n")
	return sb.String()
}

func generateFileSection(f *ProtoFile) string {
	var sb strings.Builder

	anchor := f.Path
	sb.WriteString(fmt.Sprintf("<a name=\"%s\"></a>\n", anchor))
	sb.WriteString("<p align=\"right\"><a href=\"#top\">Top</a></p>\n\n")
	sb.WriteString(fmt.Sprintf("## %s\n\n", f.Path))

	// Messages
	for _, m := range f.Messages {
		sb.WriteString(generateMessageSection(m, f.Package))
	}

	// Top-level enums
	for _, e := range f.Enums {
		sb.WriteString(generateEnumSection(e))
	}

	// Services
	for _, svc := range f.Services {
		sb.WriteString(generateServiceSection(svc, f.Package))
	}

	sb.WriteString(" \n\n")
	return sb.String()
}

func generateMessageSection(m *Message, pkg string) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("<a name=\"%s\"></a>\n\n", m.FullName))
	sb.WriteString(fmt.Sprintf("### %s\n", m.Name))
	if m.Comment != "" {
		sb.WriteString(escapeDesc(m.Comment) + "\n")
	}
	sb.WriteString("\n")

	if len(m.Fields) > 0 {
		sb.WriteString("| Field | Type | Label | Description |\n")
		sb.WriteString("| ----- | ---- | ----- | ----------- |\n")
		for _, field := range m.Fields {
			typeStr := formatType(field.Type, pkg)
			label := field.Label
			if label == "map" {
				label = ""
			}
			desc := escapeDesc(field.Comment)
			sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n", field.Name, typeStr, label, desc))
		}
		sb.WriteString("\n")
	} else {
		sb.WriteString("\n")
	}

	// Nested enums
	for _, e := range m.Enums {
		sb.WriteString(generateEnumSection(e))
	}

	// Nested messages
	for _, nested := range m.Nested {
		sb.WriteString(generateMessageSection(nested, pkg))
	}

	return sb.String()
}

func generateEnumSection(e *Enum) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("<a name=\"%s\"></a>\n\n", e.FullName))
	sb.WriteString(fmt.Sprintf("### %s\n", e.Name))
	if e.Comment != "" {
		sb.WriteString(escapeDesc(e.Comment) + "\n")
	}
	sb.WriteString("\n")

	if len(e.Values) > 0 {
		sb.WriteString("| Name | Number | Description |\n")
		sb.WriteString("| ---- | ------ | ----------- |\n")
		for _, v := range e.Values {
			desc := escapeDesc(v.Comment)
			sb.WriteString(fmt.Sprintf("| %s | %s | %s |\n", v.Name, v.Number, desc))
		}
		sb.WriteString("\n")
	} else {
		sb.WriteString("\n")
	}

	return sb.String()
}

func generateServiceSection(svc *Service, pkg string) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("<a name=\"%s.%s\"></a>\n\n", pkg, svc.Name))
	sb.WriteString(fmt.Sprintf("### %s\n", svc.Name))
	if svc.Comment != "" {
		sb.WriteString(escapeDesc(svc.Comment) + "\n")
	}
	sb.WriteString("\n")

	if len(svc.Methods) > 0 {
		sb.WriteString("| Method Name | Request Type | Response Type | Description |\n")
		sb.WriteString("| ----------- | ------------ | ------------- | ------------|\n")
		for _, m := range svc.Methods {
			input := fmtSingleType(m.Input, pkg)
			output := fmtSingleType(m.Output, pkg)
			if m.ServerStream {
				output = "stream " + output
			}
			desc := escapeDesc(m.Comment)
			sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n", m.Name, input, output, desc))
		}
		sb.WriteString("\n")
	} else {
		sb.WriteString("\n")
	}

	return sb.String()
}

// ---------- main ----------

func main() {
	output := flag.String("output", "public/omni/reference/api.mdx", "Output file path")
	flag.Parse()

	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		fmt.Fprintln(os.Stderr, "Tip: set GITHUB_TOKEN to avoid GitHub rate limits")
	}

	var files []*ProtoFile

	for _, path := range protoFiles {
		url := baseURL + path
		fmt.Fprintf(os.Stderr, "  Fetching %s...\n", path)
		content, err := fetch(url)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error fetching %s: %v\n", path, err)
			os.Exit(1)
		}
		pf := parseProto(path, content)
		files = append(files, pf)
	}

	fmt.Fprintln(os.Stderr, "  Generating MDX...")

	var sb strings.Builder
	sb.WriteString(frontmatter)
	sb.WriteString(generateTOC(files))
	sb.WriteString("\n")
	for _, f := range files {
		sb.WriteString(generateFileSection(f))
	}
	sb.WriteString(scalarTable)

	if err := os.WriteFile(*output, []byte(sb.String()), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing output: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "Done! api.mdx written to %s\n", *output)
}
