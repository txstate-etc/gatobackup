package main

import (
	"strings"
)

type ErrParseNode struct {
	node string
}

func (e ErrParseNode) Error() string {
	return "Node string: " + e.node
}

type Node struct {
	Repo string
	Name string
	Path string
}

func NewNode(s string) (*Node, error) {
	parts := strings.SplitN(s, ".", 2)
	if len(parts) != 2 || len(parts[0]) == 0 || len(parts[1]) == 0 {
		return nil, ErrParseNode{node: s}
	}
	return &Node{Repo: parts[0], Name: parts[1], Path: strings.Replace(parts[1], ".", "/", -1)}, nil
}

func (n *Node) String() string {
	return n.Repo + "." + n.Name
}
