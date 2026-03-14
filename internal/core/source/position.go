package source

import "fmt"

type Position struct {
	Index  int
	Line   int
	Column int
}

func NewPosition() Position {
	return Position{Line: 1, Column: 1}
}

func (p Position) String() string {
	return fmt.Sprintf("%d:%d", p.Line, p.Column)
}

func (p *Position) Advance(toSkip string) *Position {
	prevWasTab := false
	for _, char := range toSkip {
		switch char {
		case '\n':
			p.Line++
			p.Column = 1
			p.Index++
			prevWasTab = false
		case '\t':
			p.Column += 4
			p.Index++
			prevWasTab = true
		default:
			if !prevWasTab {
				p.Column++
			}
			p.Index += len(string(char))
			prevWasTab = false
		}
	}
	return p
}
