package wasm

import "bytes"

type ValType byte

const (
	valTypeI32 ValType = 0x7f
	valTypeI64 ValType = 0x7e
	valTypeF32 ValType = 0x7d
	valTypeF64 ValType = 0x7c
)

const (
	sectionType    = 1
	sectionImport  = 2
	sectionFunc    = 3
	sectionTable   = 4
	sectionMemory  = 5
	sectionGlobal  = 6
	sectionExport  = 7
	sectionElement = 9
	sectionCode    = 10
	sectionData    = 11
)

const (
	importKindFunc   = 0x00
	//importKindMemory = 0x02
	exportKindFunc   = 0x00
	exportKindMem    = 0x02
	exportKindGlobal = 0x03
	exportKindTable  = 0x01
)

type funcType struct {
	params  []ValType
	results []ValType
}

type importFunc struct {
	module    string
	name      string
	typeIndex uint32
}

type functionDef struct {
	typeIndex uint32
	locals    []ValType
	body      []byte
}

type exportDef struct {
	name  string
	kind  byte
	index uint32
}

type dataSegment struct {
	offset uint32
	data   []byte
}

type globalDef struct {
	valType ValType
	mutable bool
	init    []byte
}

type ModuleBuilder struct {
	types       []funcType
	importFuncs []importFunc
	functions   []functionDef
	exports     []exportDef
	data        []dataSegment
	globals     []globalDef
	tableMin    uint32
	elements    []uint32 // function indices in the table

	memoryMin uint32
}

func (m *ModuleBuilder) addType(params, results []ValType) uint32 {
	for i, t := range m.types {
		if sameTypes(t.params, params) && sameTypes(t.results, results) {
			return uint32(i)
		}
	}
	m.types = append(m.types, funcType{params: params, results: results})
	return uint32(len(m.types) - 1)
}

func (m *ModuleBuilder) addImportFunc(module, name string, typeIndex uint32) uint32 {
	m.importFuncs = append(m.importFuncs, importFunc{
		module:    module,
		name:      name,
		typeIndex: typeIndex,
	})
	return uint32(len(m.importFuncs) - 1)
}

func (m *ModuleBuilder) addFunction(typeIndex uint32, locals []ValType, body []byte) uint32 {
	m.functions = append(m.functions, functionDef{
		typeIndex: typeIndex,
		locals:    locals,
		body:      body,
	})
	return uint32(len(m.importFuncs) + len(m.functions) - 1)
}

func (m *ModuleBuilder) addExport(name string, kind byte, index uint32) {
	m.exports = append(m.exports, exportDef{name: name, kind: kind, index: index})
}

func (m *ModuleBuilder) addData(offset uint32, data []byte) {
	if len(data) == 0 {
		return
	}
	m.data = append(m.data, dataSegment{offset: offset, data: data})
}

func (m *ModuleBuilder) addGlobal(valType ValType, mutable bool, init []byte) uint32 {
	m.globals = append(m.globals, globalDef{valType: valType, mutable: mutable, init: init})
	return uint32(len(m.globals) - 1)
}

func (m *ModuleBuilder) setTableSize(min uint32) {
	m.tableMin = min
}

func (m *ModuleBuilder) addTableElement(funcIndex uint32) uint32 {
	m.elements = append(m.elements, funcIndex)
	return uint32(len(m.elements) - 1)
}

func (m *ModuleBuilder) emit() []byte {
	var out bytes.Buffer
	out.Write([]byte{0x00, 0x61, 0x73, 0x6d})
	out.Write([]byte{0x01, 0x00, 0x00, 0x00})

	if len(m.types) > 0 {
		section := bytes.Buffer{}
		section.Write(encodeU32(uint32(len(m.types))))
		for _, t := range m.types {
			section.WriteByte(0x60)
			section.Write(encodeU32(uint32(len(t.params))))
			for _, p := range t.params {
				section.WriteByte(byte(p))
			}
			section.Write(encodeU32(uint32(len(t.results))))
			for _, r := range t.results {
				section.WriteByte(byte(r))
			}
		}
		out.Write(emitSection(sectionType, section.Bytes()))
	}

	if len(m.importFuncs) > 0 {
		section := bytes.Buffer{}
		section.Write(encodeU32(uint32(len(m.importFuncs))))
		for _, imp := range m.importFuncs {
			section.Write(encodeString(imp.module))
			section.Write(encodeString(imp.name))
			section.WriteByte(importKindFunc)
			section.Write(encodeU32(imp.typeIndex))
		}
		out.Write(emitSection(sectionImport, section.Bytes()))
	}

	if len(m.functions) > 0 {
		section := bytes.Buffer{}
		section.Write(encodeU32(uint32(len(m.functions))))
		for _, fn := range m.functions {
			section.Write(encodeU32(fn.typeIndex))
		}
		out.Write(emitSection(sectionFunc, section.Bytes()))
	}

	if m.tableMin > 0 {
		section := bytes.Buffer{}
		section.Write(encodeU32(1)) // one table
		section.WriteByte(0x70)     // funcref type
		section.Write(encodeLimits(m.tableMin))
		out.Write(emitSection(sectionTable, section.Bytes()))
	}

	if m.memoryMin > 0 {
		section := bytes.Buffer{}
		section.Write(encodeU32(1))
		section.Write(encodeLimits(m.memoryMin))
		out.Write(emitSection(sectionMemory, section.Bytes()))
	}

	if len(m.globals) > 0 {
		section := bytes.Buffer{}
		section.Write(encodeU32(uint32(len(m.globals))))
		for _, g := range m.globals {
			section.WriteByte(byte(g.valType))
			if g.mutable {
				section.WriteByte(0x01)
			} else {
				section.WriteByte(0x00)
			}
			section.Write(g.init)
			section.WriteByte(0x0b)
		}
		out.Write(emitSection(sectionGlobal, section.Bytes()))
	}

	if len(m.exports) > 0 {
		section := bytes.Buffer{}
		section.Write(encodeU32(uint32(len(m.exports))))
		for _, exp := range m.exports {
			section.Write(encodeString(exp.name))
			section.WriteByte(exp.kind)
			section.Write(encodeU32(exp.index))
		}
		out.Write(emitSection(sectionExport, section.Bytes()))
	}

	if len(m.elements) > 0 {
		section := bytes.Buffer{}
		section.Write(encodeU32(1)) // one element segment
		section.WriteByte(0x00)     // active with table index 0
		section.WriteByte(0x41)     // i32.const
		section.Write(encodeU32(0)) // offset 0
		section.WriteByte(0x0b)     // end
		section.Write(encodeU32(uint32(len(m.elements))))
		for _, funcIndex := range m.elements {
			section.Write(encodeU32(funcIndex))
		}
		out.Write(emitSection(sectionElement, section.Bytes()))
	}

	if len(m.functions) > 0 {
		section := bytes.Buffer{}
		section.Write(encodeU32(uint32(len(m.functions))))
		for _, fn := range m.functions {
			body := bytes.Buffer{}
			body.Write(encodeLocals(fn.locals))
			body.Write(fn.body)
			body.WriteByte(0x0b)
			section.Write(encodeU32(uint32(body.Len())))
			section.Write(body.Bytes())
		}
		out.Write(emitSection(sectionCode, section.Bytes()))
	}

	if len(m.data) > 0 {
		section := bytes.Buffer{}
		section.Write(encodeU32(uint32(len(m.data))))
		for _, seg := range m.data {
			section.WriteByte(0x00)
			section.WriteByte(0x41) // i32.const
			section.Write(encodeU32(seg.offset))
			section.WriteByte(0x0b)
			section.Write(encodeU32(uint32(len(seg.data))))
			section.Write(seg.data)
		}
		out.Write(emitSection(sectionData, section.Bytes()))
	}

	return out.Bytes()
}

func encodeString(s string) []byte {
	b := []byte(s)
	out := make([]byte, 0, len(b)+5)
	out = append(out, encodeU32(uint32(len(b)))...)
	out = append(out, b...)
	return out
}

func encodeU32(v uint32) []byte {
	var out []byte
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			b |= 0x80
		}
		out = append(out, b)
		if v == 0 {
			break
		}
	}
	return out
}

func encodeS32(v int32) []byte {
	var out []byte
	more := true
	for more {
		b := byte(v & 0x7f)
		v >>= 7
		signBit := (b & 0x40) != 0
		more = !((v == 0 && !signBit) || (v == -1 && signBit))
		if more {
			b |= 0x80
		}
		out = append(out, b)
	}
	return out
}

func encodeS64(v int64) []byte {
	var out []byte
	more := true
	for more {
		b := byte(v & 0x7f)
		v >>= 7
		signBit := (b & 0x40) != 0
		more = !((v == 0 && !signBit) || (v == -1 && signBit))
		if more {
			b |= 0x80
		}
		out = append(out, b)
	}
	return out
}

func emitSection(id byte, content []byte) []byte {
	out := make([]byte, 0, len(content)+8)
	out = append(out, id)
	out = append(out, encodeU32(uint32(len(content)))...)
	out = append(out, content...)
	return out
}

func encodeLimits(min uint32) []byte {
	out := []byte{0x00}
	out = append(out, encodeU32(min)...)
	return out
}

func encodeLocals(locals []ValType) []byte {
	if len(locals) == 0 {
		return []byte{0x00}
	}
	var groups []struct {
		count uint32
		typ   ValType
	}
	for _, typ := range locals {
		if len(groups) == 0 || groups[len(groups)-1].typ != typ {
			groups = append(groups, struct {
				count uint32
				typ   ValType
			}{count: 1, typ: typ})
		} else {
			groups[len(groups)-1].count++
		}
	}
	out := make([]byte, 0, 1+len(groups)*3)
	out = append(out, encodeU32(uint32(len(groups)))...)
	for _, g := range groups {
		out = append(out, encodeU32(g.count)...)
		out = append(out, byte(g.typ))
	}
	return out
}

func sameTypes(a, b []ValType) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
