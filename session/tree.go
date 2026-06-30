package session

import (
	"sort"
	"time"
)

// TreeNode represents a node in the session entry tree.
type TreeNode struct {
	Entry    Entry       `json:"entry"`
	Children []*TreeNode `json:"children,omitempty"`
	Label    string      `json:"label,omitempty"`
	Depth    int64       `json:"depth"`
}

// BuildTree constructs a tree from the session's entries.
// Entries with unknown parents are treated as roots.
func BuildTree(entries []Entry, labels map[string]string) []*TreeNode {
	nodeMap := make(map[string]*TreeNode)
	var roots []*TreeNode

	for _, e := range entries {
		label := ""
		if labels != nil {
			label = labels[e.ID]
		}
		node := &TreeNode{
			Entry: e,
			Label: label,
		}
		nodeMap[e.ID] = node
	}

	for _, e := range entries {
		node := nodeMap[e.ID]
		if e.ParentID == "" {
			roots = append(roots, node)
			continue
		}
		parent, ok := nodeMap[e.ParentID]
		if !ok {
			roots = append(roots, node)
			continue
		}
		parent.Children = append(parent.Children, node)
	}

	for _, n := range nodeMap {
		sort.Slice(n.Children, func(i, j int) bool {
			return n.Children[i].Entry.Timestamp.Before(n.Children[j].Entry.Timestamp)
		})
	}

	var setDepth func(node *TreeNode, depth int64)
	setDepth = func(node *TreeNode, depth int64) {
		node.Depth = depth
		for _, child := range node.Children {
			setDepth(child, depth+1)
		}
	}
	for _, root := range roots {
		setDepth(root, 0)
	}

	return roots
}

// GetTree returns the tree structure for this session.
func (m *Manager) GetTree() []*TreeNode {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return BuildTree(m.entries, m.labelsById)
}

// BranchPoints returns all entries that have more than one child (fork points).
func (m *Manager) BranchPoints() []Entry {
	m.mu.RLock()
	defer m.mu.RUnlock()

	childCount := make(map[string]int64)
	for _, e := range m.entries {
		if e.ParentID != "" {
			childCount[e.ParentID]++
		}
	}

	var points []Entry
	for _, e := range m.entries {
		if childCount[e.ID] > 1 {
			points = append(points, e)
		}
	}
	return points
}

// Leaves returns all entries with no children.
func (m *Manager) Leaves() []Entry {
	m.mu.RLock()
	defer m.mu.RUnlock()

	hasChild := make(map[string]bool)
	for _, e := range m.entries {
		if e.ParentID != "" {
			hasChild[e.ParentID] = true
		}
	}

	var leaves []Entry
	for _, e := range m.entries {
		if !hasChild[e.ID] {
			leaves = append(leaves, e)
		}
	}
	return leaves
}

// PathTo returns the ordered list of entries from root to the given entry ID.
func (m *Manager) PathTo(entryID string) []Entry {
	m.mu.RLock()
	defer m.mu.RUnlock()

	parentMap := make(map[string]string)
	entryMap := make(map[string]Entry)
	for _, e := range m.entries {
		parentMap[e.ID] = e.ParentID
		entryMap[e.ID] = e
	}

	var chain []Entry
	current := entryID
	for current != "" {
		if e, ok := entryMap[current]; ok {
			chain = append(chain, e)
		} else {
			break
		}
		current = parentMap[current]
	}

	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain
}

// TreeStats returns summary statistics about the session tree.
type TreeStats struct {
	TotalEntries    int64     `json:"total_entries"`
	MessageCount    int64     `json:"message_count"`
	BranchCount     int64     `json:"branch_count"`
	LeafCount       int64     `json:"leaf_count"`
	MaxDepth        int64     `json:"max_depth"`
	FirstEntry      time.Time `json:"first_entry"`
	LastEntry       time.Time `json:"last_entry"`
	CompactionCount int64     `json:"compaction_count"`
}

// Stats returns statistics about this session.
func (m *Manager) Stats() TreeStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stats := TreeStats{
		TotalEntries: int64(len(m.entries)),
	}

	childCount := make(map[string]int64)
	hasChild := make(map[string]bool)
	depthMap := make(map[string]int64)

	for _, e := range m.entries {
		if e.ParentID != "" {
			childCount[e.ParentID]++
			hasChild[e.ParentID] = true
		}

		switch e.Type {
		case EntryMessage:
			stats.MessageCount++
		case EntryCompaction:
			stats.CompactionCount++
		}

		if stats.FirstEntry.IsZero() || e.Timestamp.Before(stats.FirstEntry) {
			stats.FirstEntry = e.Timestamp
		}
		if e.Timestamp.After(stats.LastEntry) {
			stats.LastEntry = e.Timestamp
		}
	}

	for _, e := range m.entries {
		if e.ParentID == "" {
			depthMap[e.ID] = 0
		} else {
			depthMap[e.ID] = depthMap[e.ParentID] + 1
		}
		if depthMap[e.ID] > stats.MaxDepth {
			stats.MaxDepth = depthMap[e.ID]
		}
	}

	for id := range childCount {
		if childCount[id] > 1 {
			stats.BranchCount++
		}
	}

	for _, e := range m.entries {
		if !hasChild[e.ID] {
			stats.LeafCount++
		}
	}

	return stats
}
