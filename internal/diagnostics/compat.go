package diagnostics

type Bag = DiagnosticBag

func NewBag() *Bag {
	return NewDiagnosticBag("")
}

func (db *DiagnosticBag) All() []*Diagnostic {
	return db.Diagnostics()
}
