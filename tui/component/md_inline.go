package component

type inlineKind int

const (
	inText inlineKind = iota
	inCode
	inStrong
	inEmph
	inStrike
	inLink
	inImage
	inMath
	inBreak
	inFootnote
	inMark
)

type inlineNode struct {
	Kind inlineKind
	Text string
	URL  string
	Kids []inlineNode
}
