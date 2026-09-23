package cutsexp

import (
	"bufio"
	"io"
)

// Cell represents a card to be generated.
type Cell struct {
	Title string
}

// Node represents a node in the S-expression tree.
type Node struct {
	Value    string
	Children []*Node
}

func (n *Node) IsList() bool {
	return len(n.Children) > 0
}

func (n *Node) IsAtom() bool {
	return len(n.Children) == 0
}

// containsTask recursively checks if n or any of its descendants (through
// empty-value wrapper lists) is a task node.
func containsTask(n *Node) bool {
	if n == nil || !n.IsList() {
		return false
	}
	if len(n.Children) > 0 && n.Children[0].Value == "task" {
		return true
	}
	for _, child := range n.Children {
		if containsTask(child) {
			return true
		}
	}
	return false
}

// Parse parses an S-expression from a reader.
func Parse(r io.Reader) (*Node, error) {
	scanner := bufio.NewScanner(r)
	scanner.Split(bufio.ScanRunes)

	var root *Node
	var stack []*Node
	var currentAtom string
	inString := false
	inComment := false

	flushAtom := func() {
		if currentAtom != "" || inString {
			if len(stack) > 0 {
				last := stack[len(stack)-1]
				last.Children = append(last.Children, &Node{Value: currentAtom})
			}
			currentAtom = ""
			inString = false
		}
	}

	for scanner.Scan() {
		tok := scanner.Text()

		if inComment {
			if tok == "\n" {
				inComment = false
			}
			continue
		}

		if inString {
			if tok == "\\" {
				if scanner.Scan() {
					currentAtom += scanner.Text()
				}
				continue
			}
			if tok == "\"" {
				inString = false
				flushAtom()
			} else {
				currentAtom += tok
			}
			continue
		}

		switch tok {
		case "\"":
			inString = true
		case ";":
			inComment = true
			flushAtom()
		case "(":
			flushAtom()
			newNode := &Node{}
			if root == nil {
				root = newNode
			} else {
				last := stack[len(stack)-1]
				last.Children = append(last.Children, newNode)
			}
			stack = append(stack, newNode)
		case ")":
			flushAtom()
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
		case " ", "\n", "\t", "\r":
			flushAtom()
		default:
			currentAtom += tok
		}
	}
	flushAtom()

	return root, nil
}

// CutFromSchema parses a roadmap from r and returns cells for open leaf tasks.
func CutFromSchema(r io.Reader, leg string) ([]Cell, error) {
	node, err := Parse(r)
	if err != nil {
		return nil, err
	}

	var cells []Cell

	var find func(*Node)
	find = func(n *Node) {
		if n == nil || !n.IsList() {
			return
		}

		if len(n.Children) > 0 && n.Children[0].Value == "task" {
			isLeaf := true
			for _, child := range n.Children {
				if child.IsList() && containsTask(child) {
					isLeaf = false
					find(child)
				}
			}

			if isLeaf {
				status := ""
				title := ""
				if len(n.Children) > 1 {
					title = n.Children[1].Value
				}

				for _, prop := range n.Children {
					if prop.IsList() && len(prop.Children) > 1 && prop.Children[0].Value == "status" {
						status = prop.Children[1].Value
					}
				}

				if status == "open" {
					cells = append(cells, Cell{Title: title})
				}
			}
		} else {
			for _, child := range n.Children {
				find(child)
			}
		}
	}

	var findLeg func(*Node)
	findLeg = func(n *Node) {
		if n == nil || !n.IsList() {
			return
		}
		if len(n.Children) > 1 && n.Children[0].Value == "leg" && n.Children[1].Value == leg {
			find(n)
			return
		}
		for _, child := range n.Children {
			findLeg(child)
		}
	}

	findLeg(node)
	return cells, nil
}
