package startup

import (
	"encoding/xml"
	"errors"
	"reflect"
	"sort"
	"strings"
)

type taskXMLNode struct {
	Element  xml.StartElement
	Text     string
	Children []*taskXMLNode
}

func sameTaskXML(actual, expected string) bool {
	return sameTaskXMLForUser(actual, expected, "", nil)
}

func sameTaskXMLForUser(actual, expected, sid string, resolve func(string) (string, error)) bool {
	a, err := taskXMLTree(actual)
	if err != nil {
		return false
	}
	b, err := taskXMLTree(expected)
	if err != nil {
		return false
	}
	if normalizeTaskXML(a, "", sid, resolve) != nil || normalizeTaskXML(b, "", sid, resolve) != nil {
		return false
	}
	return reflect.DeepEqual(a, b)
}

func taskXMLTree(text string) (*taskXMLNode, error) {
	tokens, err := xmlTokens(text)
	if err != nil {
		return nil, err
	}
	var root *taskXMLNode
	var stack []*taskXMLNode
	for _, token := range tokens {
		switch value := token.(type) {
		case xml.StartElement:
			node := &taskXMLNode{Element: value}
			if len(stack) == 0 {
				if root != nil {
					return nil, errors.New("multiple task XML roots")
				}
				root = node
			} else {
				parent := stack[len(stack)-1]
				parent.Children = append(parent.Children, node)
			}
			stack = append(stack, node)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) == 0 {
				return nil, errors.New("text outside task XML root")
			}
			stack[len(stack)-1].Text += string(value)
		}
	}
	if root == nil {
		return nil, errors.New("empty task XML")
	}
	return root, nil
}

// Registration reorders schema-all containers and omits these SDK defaults.
// Never ignore unknown fields or order-sensitive lists such as Actions/Triggers.
var taskXMLDefaults = map[string]string{
	"Task/Triggers/LogonTrigger/Enabled":      "true",
	"Task/Settings/AllowHardTerminate":        "true",
	"Task/Settings/StartWhenAvailable":        "false",
	"Task/Settings/RunOnlyIfNetworkAvailable": "false",
	"Task/Settings/AllowStartOnDemand":        "true",
	"Task/Settings/Enabled":                   "true",
	"Task/Settings/Hidden":                    "false",
	"Task/Settings/RunOnlyIfIdle":             "false",
	"Task/Settings/WakeToRun":                 "false",
	"Task/Settings/Priority":                  "7",
}

func normalizeTaskXML(node *taskXMLNode, parent, sid string, resolve func(string) (string, error)) error {
	path := parent + "/" + node.Element.Name.Local
	if parent == "" {
		path = node.Element.Name.Local
	}
	if node.Element.Name.Space != "http://schemas.microsoft.com/windows/2004/02/mit/task" || strings.HasPrefix(parent, "!") {
		path = "!" + path
	}
	if sid != "" && (path == "Task/Triggers/LogonTrigger/UserId" || path == "Task/Principals/Principal/UserId") {
		if node.Text != sid {
			if resolve == nil {
				return errors.New("task user cannot be resolved")
			}
			resolved, err := resolve(node.Text)
			if err != nil || resolved != sid {
				return errors.New("task user does not match the current SID")
			}
		}
		node.Text = sid
	}
	unordered := path == "Task" || path == "Task/RegistrationInfo" || path == "Task/Settings" || path == "Task/Settings/IdleSettings"
	seen := make(map[xml.Name]bool)
	children := node.Children[:0]
	for _, child := range node.Children {
		childPath := path + "/" + child.Element.Name.Local
		defaultText, hasDefault := taskXMLDefaults[childPath]
		if unordered || hasDefault {
			if seen[child.Element.Name] {
				return errors.New("duplicate task XML field")
			}
			seen[child.Element.Name] = true
		}
		if err := normalizeTaskXML(child, path, sid, resolve); err != nil {
			return err
		}
		if hasDefault && child.Element.Name.Space == node.Element.Name.Space && len(child.Element.Attr) == 0 && len(child.Children) == 0 && child.Text == defaultText {
			continue
		}
		children = append(children, child)
	}
	node.Children = children
	if unordered {
		sort.Slice(node.Children, func(i, j int) bool {
			a, b := node.Children[i].Element.Name, node.Children[j].Element.Name
			if a.Space != b.Space {
				return a.Space < b.Space
			}
			return a.Local < b.Local
		})
	}
	return nil
}
