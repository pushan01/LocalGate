package meta

import (
	_ "embed"
	"fmt"
	"strings"
)

//go:embed prompt.j2
var embeddedPromptTemplate string

type templateNode interface {
	render(scope *templateScope) (string, error)
}

type templateTextNode struct{ text string }

func (n templateTextNode) render(scope *templateScope) (string, error) {
	_ = scope
	return n.text, nil
}

type templateValueNode struct{ expr string }

func (n templateValueNode) render(scope *templateScope) (string, error) {
	v, ok := scope.resolve(n.expr)
	if !ok || v == nil {
		return "", nil
	}
	return fmt.Sprint(v), nil
}

type templateIfNode struct {
	expr     string
	children []templateNode
}

func (n templateIfNode) render(scope *templateScope) (string, error) {
	v, ok := scope.resolve(n.expr)
	if !ok || !isTemplateTruthy(v) {
		return "", nil
	}
	return renderTemplateNodes(n.children, scope)
}

type templateForNode struct {
	varName  string
	expr     string
	children []templateNode
}

func (n templateForNode) render(scope *templateScope) (string, error) {
	v, ok := scope.resolve(n.expr)
	if !ok {
		return "", nil
	}
	items, ok := asTemplateList(v)
	if !ok {
		return "", nil
	}
	var b strings.Builder
	for _, item := range items {
		child := &templateScope{
			values: map[string]any{n.varName: item},
			parent: scope,
		}
		s, err := renderTemplateNodes(n.children, child)
		if err != nil {
			return "", err
		}
		b.WriteString(s)
	}
	return b.String(), nil
}

type templateScope struct {
	values map[string]any
	parent *templateScope
}

func (s *templateScope) resolve(expr string) (any, bool) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, false
	}
	parts := strings.Split(expr, ".")
	v, ok := s.lookup(parts[0])
	if !ok {
		return nil, false
	}
	for _, part := range parts[1:] {
		part = strings.TrimSpace(part)
		switch x := v.(type) {
		case map[string]any:
			var exists bool
			v, exists = x[part]
			if !exists {
				return nil, false
			}
		default:
			return nil, false
		}
	}
	return v, true
}

func (s *templateScope) lookup(name string) (any, bool) {
	for cur := s; cur != nil; cur = cur.parent {
		if cur.values == nil {
			continue
		}
		if v, ok := cur.values[name]; ok {
			return v, true
		}
	}
	return nil, false
}

func renderEmbeddedPromptTemplate(values map[string]any) (string, error) {
	return renderJinjaLikeTemplate(embeddedPromptTemplate, values)
}

func renderJinjaLikeTemplate(input string, values map[string]any) (string, error) {
	pos := 0
	nodes, endTag, err := parseTemplateNodes(input, &pos, nil)
	if err != nil {
		return "", err
	}
	if endTag != "" {
		return "", fmt.Errorf("unexpected template end tag: %s", endTag)
	}
	out, err := renderTemplateNodes(nodes, &templateScope{values: values})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func renderTemplateNodes(nodes []templateNode, scope *templateScope) (string, error) {
	var b strings.Builder
	for _, node := range nodes {
		s, err := node.render(scope)
		if err != nil {
			return "", err
		}
		b.WriteString(s)
	}
	return b.String(), nil
}

func parseTemplateNodes(input string, pos *int, endTags map[string]struct{}) ([]templateNode, string, error) {
	nodes := make([]templateNode, 0)
	for *pos < len(input) {
		nextVar := strings.Index(input[*pos:], "{{")
		nextTag := strings.Index(input[*pos:], "{%")
		next := earliestTemplateIndex(nextVar, nextTag)
		if next < 0 {
			nodes = append(nodes, templateTextNode{text: input[*pos:]})
			*pos = len(input)
			break
		}
		next += *pos
		if next > *pos {
			nodes = append(nodes, templateTextNode{text: input[*pos:next]})
			*pos = next
		}
		switch {
		case strings.HasPrefix(input[*pos:], "{{"):
			end := strings.Index(input[*pos+2:], "}}")
			if end < 0 {
				return nil, "", fmt.Errorf("unterminated template value")
			}
			expr := strings.TrimSpace(input[*pos+2 : *pos+2+end])
			nodes = append(nodes, templateValueNode{expr: expr})
			*pos += 2 + end + 2
		case strings.HasPrefix(input[*pos:], "{%"):
			end := strings.Index(input[*pos+2:], "%}")
			if end < 0 {
				return nil, "", fmt.Errorf("unterminated template tag")
			}
			tag := strings.TrimSpace(input[*pos+2 : *pos+2+end])
			*pos += 2 + end + 2
			if endTags != nil {
				if _, ok := endTags[tag]; ok {
					return nodes, tag, nil
				}
			}
			switch {
			case strings.HasPrefix(tag, "if "):
				expr := strings.TrimSpace(strings.TrimPrefix(tag, "if "))
				children, endTag, err := parseTemplateNodes(input, pos, map[string]struct{}{"endif": {}})
				if err != nil {
					return nil, "", err
				}
				if endTag != "endif" {
					return nil, "", fmt.Errorf("if block missing endif")
				}
				nodes = append(nodes, templateIfNode{expr: expr, children: children})
			case strings.HasPrefix(tag, "for "):
				varName, expr, err := parseTemplateForTag(tag)
				if err != nil {
					return nil, "", err
				}
				children, endTag, err := parseTemplateNodes(input, pos, map[string]struct{}{"endfor": {}})
				if err != nil {
					return nil, "", err
				}
				if endTag != "endfor" {
					return nil, "", fmt.Errorf("for block missing endfor")
				}
				nodes = append(nodes, templateForNode{varName: varName, expr: expr, children: children})
			default:
				return nil, "", fmt.Errorf("unsupported template tag: %s", tag)
			}
		}
	}
	return nodes, "", nil
}

func parseTemplateForTag(tag string) (string, string, error) {
	rest := strings.TrimSpace(strings.TrimPrefix(tag, "for "))
	parts := strings.SplitN(rest, " in ", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid for tag: %s", tag)
	}
	varName := strings.TrimSpace(parts[0])
	expr := strings.TrimSpace(parts[1])
	if varName == "" || expr == "" {
		return "", "", fmt.Errorf("invalid for tag: %s", tag)
	}
	return varName, expr, nil
}

func earliestTemplateIndex(a, b int) int {
	switch {
	case a < 0:
		return b
	case b < 0:
		return a
	case a < b:
		return a
	default:
		return b
	}
}

func isTemplateTruthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return strings.TrimSpace(x) != ""
	case []string:
		return len(x) > 0
	case []map[string]any:
		return len(x) > 0
	case []any:
		return len(x) > 0
	case int:
		return x != 0
	default:
		return true
	}
}

func asTemplateList(v any) ([]any, bool) {
	switch x := v.(type) {
	case []any:
		return x, true
	case []string:
		out := make([]any, 0, len(x))
		for _, item := range x {
			out = append(out, item)
		}
		return out, true
	case []map[string]any:
		out := make([]any, 0, len(x))
		for _, item := range x {
			out = append(out, item)
		}
		return out, true
	default:
		return nil, false
	}
}
