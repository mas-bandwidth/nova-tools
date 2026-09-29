package work

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// BodyKind distinguishes the four observed states of an issue body:
// non-empty text, empty string, explicit JSON null, and omitted key.
type BodyKind string

const (
	BodyKindPresent BodyKind = "present"
	BodyKindEmpty   BodyKind = "empty_string"
	BodyKindNull    BodyKind = "null"
	BodyKindMissing BodyKind = "missing"
)

// User models a forge user or bot identity.
type User struct {
	Login     string                 `json:"login"`
	ID        int64                  `json:"id"`
	Type      string                 `json:"type"`
	SiteAdmin bool                   `json:"site_admin,omitempty"`
	AvatarURL string                 `json:"avatar_url,omitempty"`
	Extra     map[string]interface{} `json:"-"`
}

// Label models an issue label.
type Label struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description,omitempty"`
}

// Milestone models a milestone.
type Milestone struct {
	ID           int64      `json:"id"`
	Number       int        `json:"number"`
	Title        string     `json:"title"`
	Description  string     `json:"description,omitempty"`
	State        string     `json:"state"`
	OpenIssues   int        `json:"open_issues"`
	ClosedIssues int        `json:"closed_issues"`
	CreatedAt    time.Time  `json:"created_at"`
	DueOn        *time.Time `json:"due_on,omitempty"`
	ClosedAt     *time.Time `json:"closed_at,omitempty"`
}

// CommentEdit captures the revision history of an edited comment.
type CommentEdit struct {
	EditedAt     time.Time `json:"edited_at"`
	Editor       User      `json:"editor"`
	PreviousBody string    `json:"previous_body"`
}

// Comment models an issue comment.
type Comment struct {
	ID          int64         `json:"id"`
	IssueNumber int           `json:"issue_number,omitempty"`
	User        User          `json:"user"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
	Body        string        `json:"body"`
	Edited      bool          `json:"edited"`
	EditHistory []CommentEdit `json:"edit_history,omitempty"`
}

// Issue models an issue with precise tracking of body presence and assignees.
type Issue struct {
	ID               int64      `json:"id"`
	Number           int        `json:"number"`
	Title            string     `json:"title"`
	State            string     `json:"state"`
	StateReason      *string    `json:"state_reason,omitempty"`
	Locked           bool       `json:"locked"`
	ActiveLockReason *string    `json:"active_lock_reason,omitempty"`
	User             User       `json:"user"`
	Labels           []Label    `json:"labels"`
	Assignees        []User     `json:"assignees"`
	Milestone        *Milestone `json:"milestone,omitempty"`
	Body             *string    `json:"body"`
	BodyState        BodyKind   `json:"-"`
	CommentsCount    int        `json:"-"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	ClosedAt         *time.Time `json:"closed_at,omitempty"`
	Comments         []Comment  `json:"-"`
}

// UnmarshalJSON customizes unmarshaling to distinguish null, empty, present, and missing body,
// as well as polymorphic comments (integer count in raw API vs array in nested fixtures).
func (i *Issue) UnmarshalJSON(data []byte) error {
	type Alias Issue
	aux := struct {
		*Alias
		RawBody     json.RawMessage `json:"body"`
		RawComments json.RawMessage `json:"comments"`
	}{
		Alias: (*Alias)(i),
	}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	if aux.RawBody == nil {
		i.Body = nil
		i.BodyState = BodyKindMissing
	} else {
		rawStr := string(aux.RawBody)
		if rawStr == "null" {
			i.Body = nil
			i.BodyState = BodyKindNull
		} else {
			var str string
			if err := json.Unmarshal(aux.RawBody, &str); err != nil {
				return err
			}
			i.Body = &str
			if str == "" {
				i.BodyState = BodyKindEmpty
			} else {
				i.BodyState = BodyKindPresent
			}
		}
	}

	if len(aux.RawComments) > 0 {
		var count int
		if err := json.Unmarshal(aux.RawComments, &count); err == nil {
			i.CommentsCount = count
		} else {
			var comments []Comment
			if err := json.Unmarshal(aux.RawComments, &comments); err == nil {
				i.Comments = comments
				i.CommentsCount = len(comments)
			}
		}
	}

	return nil
}

// IssueSeedDataset models the container for seed data.
type IssueSeedDataset struct {
	Repository    string    `json:"repository"`
	CommitSHA     string    `json:"commit_sha,omitempty"`
	SchemaVersion string    `json:"schema_version"`
	GeneratedAt   time.Time `json:"generated_at"`
	TotalIssues   int       `json:"total_issues"`
	TotalComments int       `json:"total_comments"`
	Issues        []Issue   `json:"issues"`
}

// LispKind represents the node kind in a lossless Lisp tree.
type LispKind int

const (
	LispObject LispKind = iota
	LispArray
	LispString
	LispNumber
	LispBoolean
	LispNull
)

// LispPair is a key-value entry in an Object node.
type LispPair struct {
	Key   string
	Value *LispNode
}

// LispNode represents a node in the lossless Lisp/JSON tree.
type LispNode struct {
	Kind     LispKind
	Pairs    []LispPair  // For LispObject
	Elements []*LispNode // For LispArray
	Str      string      // For LispString and LispNumber (preserving exact numeric lexeme)
	Bool     bool        // For LispBoolean
}

// JSONToLisp converts arbitrary JSON bytes into a lossless LispNode tree,
// preserving exact numeric lexemes without float conversion.
func JSONToLisp(data []byte) (*LispNode, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()

	var raw interface{}
	if err := dec.Decode(&raw); err != nil {
		return nil, err
	}
	return valueToLispNode(raw)
}

func valueToLispNode(v interface{}) (*LispNode, error) {
	if v == nil {
		return &LispNode{Kind: LispNull}, nil
	}
	switch val := v.(type) {
	case bool:
		return &LispNode{Kind: LispBoolean, Bool: val}, nil
	case json.Number:
		lexeme := val.String()
		if lexeme == "" {
			return nil, fmt.Errorf("empty numeric lexeme")
		}
		return &LispNode{Kind: LispNumber, Str: lexeme}, nil
	case string:
		return &LispNode{Kind: LispString, Str: val}, nil
	case []interface{}:
		node := &LispNode{Kind: LispArray, Elements: make([]*LispNode, len(val))}
		for i, el := range val {
			child, err := valueToLispNode(el)
			if err != nil {
				return nil, err
			}
			node.Elements[i] = child
		}
		return node, nil
	case map[string]interface{}:
		node := &LispNode{Kind: LispObject, Pairs: make([]LispPair, 0, len(val))}
		for k, el := range val {
			child, err := valueToLispNode(el)
			if err != nil {
				return nil, err
			}
			node.Pairs = append(node.Pairs, LispPair{Key: k, Value: child})
		}
		return node, nil
	default:
		return nil, fmt.Errorf("unsupported json value type: %T", v)
	}
}

// FormatLisp formats a LispNode into the canonical Lisp S-expression syntax:
// (object ("key" <val>)...), (array ...), (string "..."), (number "<lexeme>"), (boolean true|false), (null)
func FormatLisp(node *LispNode) string {
	if node == nil {
		return "(null)"
	}
	switch node.Kind {
	case LispNull:
		return "(null)"
	case LispBoolean:
		if node.Bool {
			return "(boolean true)"
		}
		return "(boolean false)"
	case LispNumber:
		return fmt.Sprintf("(number %q)", node.Str)
	case LispString:
		return fmt.Sprintf("(string %q)", escapeLispString(node.Str))
	case LispArray:
		var sb strings.Builder
		sb.WriteString("(array")
		for _, el := range node.Elements {
			sb.WriteString(" ")
			sb.WriteString(FormatLisp(el))
		}
		sb.WriteString(")")
		return sb.String()
	case LispObject:
		var sb strings.Builder
		sb.WriteString("(object")
		for _, pair := range node.Pairs {
			sb.WriteString(fmt.Sprintf(" (%q %s)", pair.Key, FormatLisp(pair.Value)))
		}
		sb.WriteString(")")
		return sb.String()
	default:
		return "(null)"
	}
}

func escapeLispString(s string) string {
	// Preserves literal characters, escaping quotes and backslashes
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b == '"' || b == '\\' {
			sb.WriteByte('\\')
		}
		sb.WriteByte(b)
	}
	return sb.String()
}

// LispToJSON converts a LispNode tree back to standard JSON bytes.
func LispToJSON(node *LispNode) ([]byte, error) {
	val, err := lispNodeToValue(node)
	if err != nil {
		return nil, err
	}
	return json.Marshal(val)
}

func lispNodeToValue(node *LispNode) (interface{}, error) {
	if node == nil {
		return nil, nil
	}
	switch node.Kind {
	case LispNull:
		return nil, nil
	case LispBoolean:
		return node.Bool, nil
	case LispNumber:
		return json.Number(node.Str), nil
	case LispString:
		return node.Str, nil
	case LispArray:
		arr := make([]interface{}, len(node.Elements))
		for i, el := range node.Elements {
			v, err := lispNodeToValue(el)
			if err != nil {
				return nil, err
			}
			arr[i] = v
		}
		return arr, nil
	case LispObject:
		obj := make(map[string]interface{}, len(node.Pairs))
		for _, p := range node.Pairs {
			v, err := lispNodeToValue(p.Value)
			if err != nil {
				return nil, err
			}
			obj[p.Key] = v
		}
		return obj, nil
	default:
		return nil, fmt.Errorf("unknown lisp node kind: %v", node.Kind)
	}
}

// ValidateNumberLexeme checks that a string conforms to JSON number syntax.
func ValidateNumberLexeme(s string) error {
	if s == "" {
		return fmt.Errorf("empty number lexeme")
	}
	if _, err := strconv.ParseFloat(s, 64); err != nil {
		return fmt.Errorf("invalid json number lexeme %q: %w", s, err)
	}
	return nil
}
