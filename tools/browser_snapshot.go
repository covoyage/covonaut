package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/chromedp"
)

type RefMapper struct {
	mu   sync.RWMutex
	refs map[string]string
}

func NewRefMapper() *RefMapper {
	return &RefMapper{
		refs: make(map[string]string),
	}
}

func (rm *RefMapper) Clear() {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.refs = make(map[string]string)
}

func (rm *RefMapper) Set(ref string, selector string) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.refs[ref] = selector
}

func (rm *RefMapper) Get(ref string) (string, bool) {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	s, ok := rm.refs[ref]
	return s, ok
}

func (rm *RefMapper) Count() int {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return len(rm.refs)
}

type snapshotResult struct {
	Text   string            `json:"text"`
	RefMap map[string]string `json:"refMap"`
}

const jsSnapshotScript = `
(function() {
	const interactiveRoles = new Set([
		'link', 'button', 'textbox', 'combobox', 'listbox',
		'checkbox', 'radio', 'slider', 'spinbutton', 'searchbox',
		'menuitem', 'menuitemcheckbox', 'menuitemradio', 'tab',
		'treeitem', 'option', 'switch', 'img', 'image'
	]);

	let refCounter = 0;
	const refMap = {};

	function getXPath(el) {
		if (!el || el.nodeType !== 1) return '';
		if (el.id) return '//*[@id="' + el.id + '"]';
		const parts = [];
		while (el && el.nodeType === 1) {
			let siblingIndex = 1;
			let sibling = el.previousSibling;
			while (sibling) {
				if (sibling.nodeType === 1 && sibling.tagName === el.tagName) siblingIndex++;
				sibling = sibling.previousSibling;
			}
			const tagName = el.tagName.toLowerCase();
			parts.unshift(siblingIndex > 1 ? tagName + '[' + siblingIndex + ']' : tagName);
			el = el.parentNode;
		}
		return '/' + parts.join('/');
	}

	function getNodeRole(el) {
		const role = el.getAttribute('role');
		if (role) return role;
		const tag = el.tagName.toLowerCase();
		const type = (el.getAttribute('type') || '').toLowerCase();
		switch (tag) {
			case 'a': return 'link';
			case 'button': return 'button';
			case 'input':
				if (['text','email','password','search','tel','url'].includes(type)) return 'textbox';
				if (type === 'checkbox') return 'checkbox';
				if (type === 'radio') return 'radio';
				if (type === 'submit' || type === 'reset') return 'button';
				return 'textbox';
			case 'select': return 'combobox';
			case 'textarea': return 'textbox';
			case 'img': return 'img';
			case 'h1': case 'h2': case 'h3': case 'h4': case 'h5': case 'h6': return 'heading';
			case 'p': return 'paragraph';
			case 'ul': case 'ol': return 'list';
			case 'li': return 'listitem';
			case 'table': return 'table';
			case 'tr': return 'row';
			case 'td': case 'th': return 'cell';
			case 'nav': return 'navigation';
			case 'main': return 'main';
			case 'header': return 'banner';
			case 'footer': return 'contentinfo';
			case 'article': return 'article';
			case 'section': return 'region';
			case 'form': return 'form';
			default: return '';
		}
	}

	function getNodeName(el) {
		const ariaLabel = el.getAttribute('aria-label');
		if (ariaLabel) return ariaLabel;
		const alt = el.getAttribute('alt');
		if (alt) return alt;
		const title = el.getAttribute('title');
		if (title) return title;
		const placeholder = el.getAttribute('placeholder');
		if (placeholder) return placeholder;
		const name = el.getAttribute('name');
		if (name) return name;
		if (el.tagName.toLowerCase() === 'a') {
			return el.textContent.trim().substring(0, 80);
		}
		return '';
	}

	function getNodeValue(el) {
		if (el.tagName.toLowerCase() === 'input' || el.tagName.toLowerCase() === 'textarea') {
			return el.value || '';
		}
		return el.getAttribute('value') || '';
	}

	function buildTree(el, lines, showAll) {
		if (!el) return;
		const role = getNodeRole(el);
		if (!role || role === 'none' || role === 'presentation') {
			for (const child of el.children) buildTree(child, lines, showAll);
			return;
		}
		const name = getNodeName(el);
		const value = getNodeValue(el);
		const isInteractive = interactiveRoles.has(role);
		if (showAll || isInteractive) {
			refCounter++;
			const ref = '@e' + refCounter;
			const xpath = getXPath(el);
			refMap[ref] = xpath;
			let line = ref + ' ' + role;
			if (name) line += ' "' + name.substring(0, 80) + '"';
			if (value && value !== name) line += ' [' + value.substring(0, 60) + ']';
			if (el.checked !== undefined && el.checked !== null) {
				if (el.checked) line += ' (checked)';
				else if (el.type === 'checkbox' || el.type === 'radio') line += ' (unchecked)';
			}
			lines.push(line);
		}
		for (const child of el.children) buildTree(child, lines, showAll);
	}

	const lines = [];
	lines.push('@e0 document');
	const body = document.body;
	if (body) {
		for (const child of body.children) buildTree(child, lines, __SHOW_ALL__);
	}
	window.__covoRefMap = refMap;
	return JSON.stringify({ text: lines.join('\n'), refMap: refMap });
})();
`

const jsRefMapOnlyScript = `
(function() {
	const interactiveRoles = new Set([
		'link', 'button', 'textbox', 'combobox', 'listbox',
		'checkbox', 'radio', 'slider', 'spinbutton', 'searchbox',
		'menuitem', 'menuitemcheckbox', 'menuitemradio', 'tab',
		'treeitem', 'option', 'switch', 'img', 'image'
	]);
	let refCounter = 0;
	const refMap = {};

	function getXPath(el) {
		if (!el || el.nodeType !== 1) return '';
		if (el.id) return '//*[@id="' + el.id + '"]';
		const parts = [];
		while (el && el.nodeType === 1) {
			let siblingIndex = 1;
			let sibling = el.previousSibling;
			while (sibling) {
				if (sibling.nodeType === 1 && sibling.tagName === el.tagName) siblingIndex++;
				sibling = sibling.previousSibling;
			}
			const tagName = el.tagName.toLowerCase();
			parts.unshift(siblingIndex > 1 ? tagName + '[' + siblingIndex + ']' : tagName);
			el = el.parentNode;
		}
		return '/' + parts.join('/');
	}

	function getNodeRole(el) {
		const role = el.getAttribute('role');
		if (role) return role;
		const tag = el.tagName.toLowerCase();
		const type = (el.getAttribute('type') || '').toLowerCase();
		switch (tag) {
			case 'a': return 'link';
			case 'button': return 'button';
			case 'input':
				if (['text','email','password','search','tel','url'].includes(type)) return 'textbox';
				if (type === 'checkbox') return 'checkbox';
				if (type === 'radio') return 'radio';
				if (type === 'submit' || type === 'reset') return 'button';
				return 'textbox';
			case 'select': return 'combobox';
			case 'textarea': return 'textbox';
			case 'img': return 'img';
			case 'h1': case 'h2': case 'h3': case 'h4': case 'h5': case 'h6': return 'heading';
			case 'p': return 'paragraph';
			case 'ul': case 'ol': return 'list';
			case 'li': return 'listitem';
			case 'table': return 'table';
			case 'tr': return 'row';
			case 'td': case 'th': return 'cell';
			case 'nav': return 'navigation';
			case 'main': return 'main';
			case 'header': return 'banner';
			case 'footer': return 'contentinfo';
			case 'article': return 'article';
			case 'section': return 'region';
			case 'form': return 'form';
			default: return '';
		}
	}

	function walk(el) {
		if (!el) return;
		const role = getNodeRole(el);
		const isInteractive = interactiveRoles.has(role);
		if (role && role !== 'none' && role !== 'presentation' && isInteractive) {
			refCounter++;
			refMap['@e' + refCounter] = getXPath(el);
		}
		for (const child of el.children) walk(child);
	}
	walk(document.body);
	window.__covoRefMap = refMap;
	return JSON.stringify(refMap);
})();
`

func generateSnapshot(ctx context.Context, full bool, refMapper *RefMapper) (string, error) {
	return GeneratePageSnapshot(ctx, full, refMapper, "default")
}

func GeneratePageSnapshot(ctx context.Context, full bool, refMapper *RefMapper, mode string) (string, error) {
	if mode == "aria" {
		ariaText, err := generateAriaSnapshot(ctx)
		if err != nil {
			return "", err
		}

		jsRefMap, jsErr := extractRefMapFromJS(ctx)
		if jsErr == nil && refMapper != nil {
			refMapper.Clear()
			for ref, xpath := range jsRefMap {
				refMapper.Set(ref, xpath)
			}
		}

		if len(ariaText) > 8000 {
			ariaText = ariaText[:8000] + "\n\n[... aria snapshot truncated to 8000 chars]"
		}
		return ariaText, nil
	}

	script := strings.Replace(jsSnapshotScript, "__SHOW_ALL__", fmt.Sprintf("%t", full), 1)
	var resultJSON string
	if err := chromedp.Run(ctx, chromedp.Evaluate(script, &resultJSON)); err != nil {
		return "", fmt.Errorf("failed to generate snapshot: %w", err)
	}

	var result snapshotResult
	if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
		return "", fmt.Errorf("failed to parse snapshot JSON: %w", err)
	}

	if refMapper != nil {
		refMapper.Clear()
		for ref, xpath := range result.RefMap {
			refMapper.Set(ref, xpath)
		}
	}

	if len(result.Text) > 8000 {
		result.Text = result.Text[:8000] + "\n\n[... snapshot truncated to 8000 chars]"
	}

	return result.Text, nil
}

func extractRefMapFromJS(ctx context.Context) (map[string]string, error) {
	var resultJSON string
	if err := chromedp.Run(ctx, chromedp.Evaluate(jsRefMapOnlyScript, &resultJSON)); err != nil {
		return nil, err
	}
	var refMap map[string]string
	if err := json.Unmarshal([]byte(resultJSON), &refMap); err != nil {
		return nil, err
	}
	return refMap, nil
}

type AccessibilityNode struct {
	Role     string
	Name     string
	Value    string
	Ref      string
	Children []*AccessibilityNode
}

func (n *AccessibilityNode) ToTreeString(indent int, maxDepth int, showAll bool) string {
	if maxDepth <= 0 {
		return ""
	}

	var sb strings.Builder
	prefix := strings.Repeat("  ", indent)

	isInteractive := isInteractiveRole(n.Role)
	shouldShow := showAll || isInteractive || n.Ref != ""

	if shouldShow {
		sb.WriteString(prefix)
		sb.WriteString(n.Ref)
		sb.WriteString(" ")
		sb.WriteString(n.Role)
		if n.Name != "" {
			sb.WriteString(" \"")
			sb.WriteString(truncateString(n.Name, 80))
			sb.WriteString("\"")
		}
		if n.Value != "" && n.Value != n.Name {
			sb.WriteString(" [")
			sb.WriteString(truncateString(n.Value, 60))
			sb.WriteString("]")
		}
		sb.WriteString("\n")
	}

	for _, child := range n.Children {
		sb.WriteString(child.ToTreeString(indent+1, maxDepth-1, showAll))
	}

	return sb.String()
}

func isInteractiveRole(role string) bool {
	switch role {
	case "link", "button", "textbox", "combobox", "listbox",
		"checkbox", "radio", "slider", "spinbutton", "searchbox",
		"menuitem", "menuitemcheckbox", "menuitemradio", "tab",
		"treeitem", "option", "switch", "img", "image":
		return true
	}
	return false
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

type jsNode struct {
	Role     string   `json:"role"`
	Name     string   `json:"name"`
	Value    string   `json:"value"`
	Ref      string   `json:"ref"`
	Children []jsNode `json:"children"`
}

func buildAccessibilityTreeFromJS(ctx context.Context) (*AccessibilityNode, error) {
	jsTreeScript := `
(function() {
	const interactiveRoles = new Set([
		'link', 'button', 'textbox', 'combobox', 'listbox',
		'checkbox', 'radio', 'slider', 'spinbutton', 'searchbox',
		'menuitem', 'menuitemcheckbox', 'menuitemradio', 'tab',
		'treeitem', 'option', 'switch', 'img', 'image'
	]);

	let refCounter = 0;

	function getNodeRole(el) {
		const role = el.getAttribute('role');
		if (role) return role;
		const tag = el.tagName.toLowerCase();
		const type = (el.getAttribute('type') || '').toLowerCase();
		switch (tag) {
			case 'a': return 'link';
			case 'button': return 'button';
			case 'input':
				if (['text','email','password','search','tel','url'].includes(type)) return 'textbox';
				if (type === 'checkbox') return 'checkbox';
				if (type === 'radio') return 'radio';
				if (type === 'submit' || type === 'reset') return 'button';
				return 'textbox';
			case 'select': return 'combobox';
			case 'textarea': return 'textbox';
			case 'img': return 'img';
			case 'h1': case 'h2': case 'h3': case 'h4': case 'h5': case 'h6': return 'heading';
			case 'p': return 'paragraph';
			case 'ul': case 'ol': return 'list';
			case 'li': return 'listitem';
			case 'table': return 'table';
			case 'tr': return 'row';
			case 'td': case 'th': return 'cell';
			case 'nav': return 'navigation';
			case 'main': return 'main';
			case 'header': return 'banner';
			case 'footer': return 'contentinfo';
			case 'article': return 'article';
			case 'section': return 'region';
			case 'form': return 'form';
			default: return '';
		}
	}

	function getNodeName(el) {
		const ariaLabel = el.getAttribute('aria-label');
		if (ariaLabel) return ariaLabel;
		const alt = el.getAttribute('alt');
		if (alt) return alt;
		const title = el.getAttribute('title');
		if (title) return title;
		const placeholder = el.getAttribute('placeholder');
		if (placeholder) return placeholder;
		const name = el.getAttribute('name');
		if (name) return name;
		if (el.tagName.toLowerCase() === 'a') {
			return el.textContent.trim().substring(0, 80);
		}
		return '';
	}

	function getNodeValue(el) {
		if (el.tagName.toLowerCase() === 'input' || el.tagName.toLowerCase() === 'textarea') {
			return el.value || '';
		}
		return el.getAttribute('value') || '';
	}

	function buildTree(el) {
		if (!el) return null;

		const role = getNodeRole(el);
		if (!role || role === 'none' || role === 'presentation') {
			const children = [];
			for (const child of el.children) {
				const tree = buildTree(child);
				if (tree) children.push(tree);
			}
			return children.length > 0 ? { role: 'group', name: '', value: '', ref: '', children } : null;
		}

		refCounter++;
		const node = {
			role: role,
			name: getNodeName(el),
			value: getNodeValue(el),
			ref: '@e' + refCounter,
			children: []
		};

		for (const child of el.children) {
			const tree = buildTree(child);
			if (tree) node.children.push(tree);
		}

		return node;
	}

	const root = {
		role: 'document',
		name: document.title || '',
		value: '',
		ref: '@e0',
		children: []
	};

	const body = document.body;
	if (body) {
		for (const child of body.children) {
			const tree = buildTree(child);
			if (tree) root.children.push(tree);
		}
	}

	return JSON.stringify(root);
})();
`

	var jsonStr string
	if err := chromedp.Run(ctx, chromedp.Evaluate(jsTreeScript, &jsonStr)); err != nil {
		return nil, fmt.Errorf("failed to build accessibility tree: %w", err)
	}

	var jsTree jsNode
	if err := json.Unmarshal([]byte(jsonStr), &jsTree); err != nil {
		return nil, fmt.Errorf("failed to parse accessibility tree: %w", err)
	}

	return convertJSTree(&jsTree), nil
}

func convertJSTree(js *jsNode) *AccessibilityNode {
	if js == nil {
		return nil
	}

	node := &AccessibilityNode{
		Role:  js.Role,
		Name:  js.Name,
		Value: js.Value,
		Ref:   js.Ref,
	}

	for _, child := range js.Children {
		node.Children = append(node.Children, convertJSTree(&child))
	}

	return node
}

func generateAriaSnapshot(ctx context.Context) (string, error) {
	nodes, err := accessibility.GetFullAXTree().Do(ctx)
	if err != nil {
		return "", fmt.Errorf("aria snapshot failed: %w", err)
	}

	childMap := make(map[accessibility.NodeID][]*accessibility.Node)
	var roots []*accessibility.Node

	for _, n := range nodes {
		if n.ParentID == "" {
			roots = append(roots, n)
		} else {
			childMap[n.ParentID] = append(childMap[n.ParentID], n)
		}
	}

	var lines []string
	refCounter := 0
	INTERACTIVE_ROLES := map[string]bool{
		"button": true, "link": true, "textbox": true, "combobox": true,
		"listbox": true, "checkbox": true, "radio": true, "slider": true,
		"spinbutton": true, "searchbox": true, "menuitem": true, "tab": true,
		"treeitem": true, "option": true, "switch": true, "img": true,
	}

	var walk func(n *accessibility.Node, depth int)
	walk = func(n *accessibility.Node, depth int) {
		if n == nil || n.Ignored {
			for _, child := range childMap[n.NodeID] {
				walk(child, depth)
			}
			return
		}

		roleName := extractAXValue(n.Role)
		chromeRole := extractAXValue(n.ChromeRole)
		if roleName == "" {
			roleName = chromeRole
		}

		skipRoles := map[string]bool{
			"": true, "none": true, "presentation": true,
			"InlineTextBox": true, "text span": true, "listitem": true,
			"paragraph": true, "heading": true,
		}
		if skipRoles[roleName] {
			for _, child := range childMap[n.NodeID] {
				walk(child, depth)
			}
			return
		}

		nodeName := extractAXValue(n.Name)
		nodeValue := extractAXValue(n.Value)

		refCounter++
		ref := "@e" + strconv.Itoa(refCounter)

		show := INTERACTIVE_ROLES[roleName]
		if !show {
			for _, child := range childMap[n.NodeID] {
				walk(child, depth + 1)
			}
			return
		}

		indent := strings.Repeat("  ", depth)
		line := indent + ref + " " + roleName
		if nodeName != "" {
			line += " \"" + truncateString(nodeName, 80) + "\""
		}
		if nodeValue != "" && nodeValue != nodeName {
			line += " [" + truncateString(nodeValue, 60) + "]"
		}
		lines = append(lines, line)

		for _, child := range childMap[n.NodeID] {
			walk(child, depth+1)
		}
	}

	for _, root := range roots {
		walk(root, 0)
	}

	return strings.Join(lines, "\n"), nil
}

func extractAXValue(v *accessibility.Value) string {
	if v == nil {
		return ""
	}
	raw := strings.Trim(v.Value.String(), "\"")
	return raw
}

