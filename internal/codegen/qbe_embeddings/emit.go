package qbe

import (
	"fmt"
	"strings"

	"compiler/internal/context_v2"
	"compiler/internal/mir"
	"compiler/internal/semantics/symbols"
	"compiler/internal/source"
	"compiler/internal/tokens"
	"compiler/internal/types"
	"compiler/internal/utils/numeric"
	ustrings "compiler/internal/utils/strings"
)

type callArg struct {
	name string
	typ  string
	sem  types.SemType
}

func (g *Generator) emitConst(c *mir.Const) {
	if c == nil {
		return
	}
	qbeType, err := g.qbeType(c.Type)
	if err != nil {
		g.reportError(err.Error(), &c.Location)
		return
	}
	value, err := g.constValue(c.Type, c.Value)
	if err != nil {
		g.reportError(err.Error(), &c.Location)
		return
	}
	g.emitLine(fmt.Sprintf("%s =%s copy %s", g.valueName(c.Result), qbeType, value))
	g.valueTypes[c.Result] = c.Type
}

func (g *Generator) emitBinary(b *mir.Binary) {
	if b == nil {
		return
	}
	left := g.valueName(b.Left)
	right := g.valueName(b.Right)
	resultName := g.valueName(b.Result)

	switch b.Op {
	case tokens.PLUS_TOKEN, tokens.MINUS_TOKEN, tokens.MUL_TOKEN, tokens.DIV_TOKEN, tokens.MOD_TOKEN,
		tokens.BIT_AND_TOKEN, tokens.BIT_OR_TOKEN, tokens.BIT_XOR_TOKEN, tokens.AND_TOKEN, tokens.OR_TOKEN:
		qbeType, err := g.qbeType(b.Type)
		if err != nil {
			g.reportError(err.Error(), &b.Location)
			return
		}
		op, err := g.binaryOp(b.Op, b.Type)
		if err != nil {
			g.reportError(err.Error(), &b.Location)
			return
		}
		g.emitLine(fmt.Sprintf("%s =%s %s %s, %s", resultName, qbeType, op, left, right))
		g.valueTypes[b.Result] = b.Type
	case tokens.EXP_TOKEN:
		// Power operator - just call ferret_pow(d, d) -> d
		// Pass values as doubles, C handles the math
		g.emitLine(fmt.Sprintf("%s =d call $ferret_pow(d %s, d %s)", resultName, left, right))
		g.valueTypes[b.Result] = types.TypeF64
	case tokens.LESS_TOKEN, tokens.LESS_EQUAL_TOKEN, tokens.GREATER_TOKEN, tokens.GREATER_EQUAL_TOKEN,
		tokens.DOUBLE_EQUAL_TOKEN, tokens.NOT_EQUAL_TOKEN:
		// Get operand types to determine comparison type
		leftType := g.valueTypes[b.Left]
		rightType := g.valueTypes[b.Right]

		// Determine the comparison type - prefer the wider type
		operandType := leftType
		if operandType == nil {
			operandType = rightType
		}
		if operandType == nil {
			operandType = b.Type // fallback
		}

		// String comparison needs special handling - call strcmp
		if operandType != nil && operandType.Equals(types.TypeString) {
			// Call ferret_strcmp(left, right) and check if result == 0
			cmpResult := g.newTemp()
			g.emitLine(fmt.Sprintf("%s =w call $ferret_strcmp(l %s, l %s)", cmpResult, left, right))
			switch b.Op {
			case tokens.DOUBLE_EQUAL_TOKEN:
				g.emitLine(fmt.Sprintf("%s =w ceqw %s, 0", resultName, cmpResult))
			case tokens.NOT_EQUAL_TOKEN:
				g.emitLine(fmt.Sprintf("%s =w cnew %s, 0", resultName, cmpResult))
			default:
				g.reportError("string comparison only supports == and !=", &b.Location)
			}
			g.valueTypes[b.Result] = types.TypeBool
			return
		}

		// For integer comparisons, ensure both operands have the same QBE type
		// When one operand is i64 and other is i32, extend the narrower one
		if leftType != nil && rightType != nil {
			leftQbe, _ := g.qbeType(leftType)
			rightQbe, _ := g.qbeType(rightType)
			if leftQbe == "l" && rightQbe == "w" {
				// Extend right operand from word to long
				extRight := g.newTemp()
				if g.isSigned(rightType) {
					g.emitLine(fmt.Sprintf("%s =l extsw %s", extRight, right))
				} else {
					g.emitLine(fmt.Sprintf("%s =l extuw %s", extRight, right))
				}
				right = extRight
				operandType = leftType
			} else if leftQbe == "w" && rightQbe == "l" {
				// Extend left operand from word to long
				extLeft := g.newTemp()
				if g.isSigned(leftType) {
					g.emitLine(fmt.Sprintf("%s =l extsw %s", extLeft, left))
				} else {
					g.emitLine(fmt.Sprintf("%s =l extuw %s", extLeft, left))
				}
				left = extLeft
				operandType = rightType
			}
		}

		op, err := g.compareOp(b.Op, operandType)
		if err != nil {
			g.reportError(err.Error(), &b.Location)
			return
		}
		g.emitLine(fmt.Sprintf("%s =w %s %s, %s", resultName, op, left, right))
		g.valueTypes[b.Result] = types.TypeBool
	default:
		g.reportUnsupported(fmt.Sprintf("binary op %s", b.Op), &b.Location)
	}
}

func (g *Generator) emitUnary(u *mir.Unary) {
	if u == nil {
		return
	}
	operand := g.valueName(u.X)
	resultName := g.valueName(u.Result)

	switch u.Op {
	case tokens.NOT_TOKEN:
		op, err := g.compareOp(tokens.DOUBLE_EQUAL_TOKEN, g.valueTypes[u.X])
		if err != nil {
			g.reportError(err.Error(), &u.Location)
			return
		}
		g.emitLine(fmt.Sprintf("%s =w %s %s, 0", resultName, op, operand))
		g.valueTypes[u.Result] = types.TypeBool
	case tokens.MINUS_TOKEN:
		qbeType, err := g.qbeType(u.Type)
		if err != nil {
			g.reportError(err.Error(), &u.Location)
			return
		}
		zero := "0"
		if g.isFloat(u.Type) {
			if qbeType == "s" {
				zero = "s_0"
			} else {
				zero = "d_0"
			}
		}
		g.emitLine(fmt.Sprintf("%s =%s sub %s, %s", resultName, qbeType, zero, operand))
		g.valueTypes[u.Result] = u.Type
	case tokens.PLUS_TOKEN:
		qbeType, err := g.qbeType(u.Type)
		if err != nil {
			g.reportError(err.Error(), &u.Location)
			return
		}
		g.emitLine(fmt.Sprintf("%s =%s copy %s", resultName, qbeType, operand))
		g.valueTypes[u.Result] = u.Type
	case tokens.BIT_NOT_TOKEN:
		qbeType, err := g.qbeType(u.Type)
		if err != nil {
			g.reportError(err.Error(), &u.Location)
			return
		}
		g.emitLine(fmt.Sprintf("%s =%s xor %s, -1", resultName, qbeType, operand))
		g.valueTypes[u.Result] = u.Type
	default:
		g.reportUnsupported(fmt.Sprintf("unary op %s", u.Op), &u.Location)
	}
}

func (g *Generator) emitCast(c *mir.Cast) {
	if c == nil {
		return
	}
	fromType := g.valueTypes[c.X]
	toType := c.Type
	if fromType == nil || toType == nil {
		g.reportUnsupported("cast", &c.Location)
		return
	}

	if isBoolType(toType) {
		op, err := g.compareOp(tokens.NOT_EQUAL_TOKEN, fromType)
		if err != nil {
			g.reportError(err.Error(), &c.Location)
			return
		}
		zero := "0"
		if g.isFloat(fromType) {
			if fromQ, err := g.qbeType(fromType); err == nil {
				switch fromQ {
				case "s":
					zero = "s_0"
				case "d":
					zero = "d_0"
				}
			}
		}
		resultName := g.valueName(c.Result)
		operand := g.valueName(c.X)
		g.emitLine(fmt.Sprintf("%s =w %s %s, %s", resultName, op, operand, zero))
		g.valueTypes[c.Result] = toType
		return
	}

	fromQ, err := g.qbeType(fromType)
	if err != nil {
		g.reportError(err.Error(), &c.Location)
		return
	}
	toQ, err := g.qbeType(toType)
	if err != nil {
		g.reportError(err.Error(), &c.Location)
		return
	}
	resultName := g.valueName(c.Result)
	operand := g.valueName(c.X)

	if _, ok := types.UnwrapType(fromType).(*types.ReferenceType); ok && g.isInteger(toType) {
		switch toQ {
		case "l":
			g.emitLine(fmt.Sprintf("%s =l copy %s", resultName, operand))
		case "w":
			g.emitLine(fmt.Sprintf("%s =w copy %s", resultName, operand))
		default:
			g.reportUnsupported("cast", &c.Location)
			return
		}
		g.valueTypes[c.Result] = toType
		return
	}

	if fromQ == toQ {
		if g.isInteger(fromType) && g.isInteger(toType) {
			if g.handleIntegerCast(resultName, operand, fromQ, fromType, toQ, toType) {
				g.valueTypes[c.Result] = toType
				return
			}
		}
		g.emitLine(fmt.Sprintf("%s =%s copy %s", resultName, toQ, operand))
		g.valueTypes[c.Result] = toType
		return
	}

	if g.isFloat(fromType) && g.isFloat(toType) {
		op := "exts"
		if fromQ == "d" && toQ == "s" {
			op = "truncd"
		}
		g.emitLine(fmt.Sprintf("%s =%s %s %s", resultName, toQ, op, operand))
		g.valueTypes[c.Result] = toType
		return
	}

	if g.isInteger(fromType) && g.isInteger(toType) {
		if g.handleIntegerCast(resultName, operand, fromQ, fromType, toQ, toType) {
			g.valueTypes[c.Result] = toType
			return
		}
		g.emitLine(fmt.Sprintf("%s =%s copy %s", resultName, toQ, operand))
		g.valueTypes[c.Result] = toType
		return
	}

	if g.isInteger(fromType) && g.isFloat(toType) {
		if g.isUnsigned(fromType) {
			if g.emitUnsignedIntToFloat(resultName, operand, fromType, fromQ, toQ) {
				g.valueTypes[c.Result] = toType
				return
			}
		}
		if isBoolType(fromType) {
			op := "swtof"
			if fromQ == "l" {
				op = "sltof"
			}
			g.emitLine(fmt.Sprintf("%s =%s %s %s", resultName, toQ, op, operand))
			g.valueTypes[c.Result] = toType
			return
		}
		op := "swtof"
		if fromQ == "l" {
			op = "sltof"
		}
		g.emitLine(fmt.Sprintf("%s =%s %s %s", resultName, toQ, op, operand))
		g.valueTypes[c.Result] = toType
		return
	}

	if g.isFloat(fromType) && g.isInteger(toType) {
		if g.isUnsigned(toType) {
			if g.emitFloatToUnsigned(resultName, operand, toType, fromQ, toQ) {
				g.valueTypes[c.Result] = toType
				return
			}
		}
		op := "stosi"
		if fromQ == "d" {
			op = "dtosi"
		}
		g.emitLine(fmt.Sprintf("%s =%s %s %s", resultName, toQ, op, operand))
		g.valueTypes[c.Result] = toType
		return
	}

	g.reportUnsupported("cast", &c.Location)
}

func isBoolType(typ types.SemType) bool {
	if typ == nil {
		return false
	}
	typ = types.UnwrapType(typ)
	if prim, ok := typ.(*types.PrimitiveType); ok {
		return prim.GetName() == types.TYPE_BOOL
	}
	return false
}

func intBitSize(typ types.SemType) int {
	if typ == nil {
		return 0
	}
	typ = types.UnwrapType(typ)
	if prim, ok := typ.(*types.PrimitiveType); ok {
		return int(types.GetNumberBitSize(prim.GetName()))
	}
	return 0
}

func (g *Generator) handleIntegerCast(resultName, operand, fromQ string, fromType types.SemType, toQ string, toType types.SemType) bool {
	toBits := intBitSize(toType)
	if toBits > 0 && toBits < 32 {
		opVal := operand
		if fromQ == "l" {
			tmp := g.newTemp()
			g.emitLine(fmt.Sprintf("%s =w copy %s", tmp, operand))
			opVal = tmp
		}
		shift := 32 - toBits
		if g.isSigned(toType) {
			tmp := g.newTemp()
			g.emitLine(fmt.Sprintf("%s =w shl %s, %d", tmp, opVal, shift))
			g.emitLine(fmt.Sprintf("%s =w sar %s, %d", resultName, tmp, shift))
		} else {
			mask := (1 << toBits) - 1
			g.emitLine(fmt.Sprintf("%s =w and %s, %d", resultName, opVal, mask))
		}
		return true
	}

	if fromQ == "w" && toQ == "l" {
		op := "extsw"
		if g.isUnsigned(fromType) {
			op = "extuw"
		}
		g.emitLine(fmt.Sprintf("%s =l %s %s", resultName, op, operand))
		return true
	}
	return false
}

func (g *Generator) emitUnsignedIntToFloat(resultName, operand string, fromType types.SemType, fromQ, toQ string) bool {
	fromBits := intBitSize(fromType)
	if fromBits == 0 || fromBits <= 16 {
		return false
	}

	funcName := ""
	switch fromBits {
	case 32:
		if toQ == "s" {
			funcName = "ferret_cast_u32_to_f32"
		} else {
			funcName = "ferret_cast_u32_to_f64"
		}
	case 64:
		if toQ == "s" {
			funcName = "ferret_cast_u64_to_f32"
		} else {
			funcName = "ferret_cast_u64_to_f64"
		}
	default:
		return false
	}

	g.emitLine(fmt.Sprintf("%s =%s call $%s(%s %s)", resultName, toQ, funcName, fromQ, operand))
	return true
}

func (g *Generator) emitFloatToUnsigned(resultName, operand string, toType types.SemType, fromQ, toQ string) bool {
	toBits := intBitSize(toType)
	if toBits == 0 {
		return false
	}

	if toBits >= 32 {
		funcName := ""
		switch toBits {
		case 32:
			if fromQ == "s" {
				funcName = "ferret_cast_f32_to_u32"
			} else {
				funcName = "ferret_cast_f64_to_u32"
			}
		case 64:
			if fromQ == "s" {
				funcName = "ferret_cast_f32_to_u64"
			} else {
				funcName = "ferret_cast_f64_to_u64"
			}
		default:
			return false
		}
		g.emitLine(fmt.Sprintf("%s =%s call $%s(%s %s)", resultName, toQ, funcName, fromQ, operand))
		return true
	}

	op := "stosi"
	if fromQ == "d" {
		op = "dtosi"
	}
	tmp := g.newTemp()
	g.emitLine(fmt.Sprintf("%s =w %s %s", tmp, op, operand))
	mask := (1 << toBits) - 1
	g.emitLine(fmt.Sprintf("%s =w and %s, %d", resultName, tmp, mask))
	return true
}

func (g *Generator) emitAlloca(a *mir.Alloca) {
	if a == nil {
		return
	}
	size := g.layout.SizeOf(a.Type)
	if size < 0 {
		g.reportUnsupported("alloca type", &a.Location)
		return
	}
	align := g.layout.AlignOf(a.Type)
	op := "alloc4"
	if align > 4 && align <= 8 {
		op = "alloc8"
	} else if align > 8 {
		op = "alloc16"
	}
	g.emitLine(fmt.Sprintf("%s =l %s %d", g.valueName(a.Result), op, size))
	g.valueTypes[a.Result] = types.NewReference(a.Type)
}

func (g *Generator) emitLoad(l *mir.Load) {
	if l == nil {
		return
	}
	if resType, ok := g.resultType(l.Type); ok {
		size := g.layout.SizeOf(resType)
		if size <= 0 {
			g.reportUnsupported("result load", &l.Location)
			return
		}
		align := g.layout.AlignOf(resType)
		op := g.allocOp(align)
		g.emitLine(fmt.Sprintf("%s =l %s %d", g.valueName(l.Result), op, size))
		g.emitMemcpy(g.valueName(l.Result), g.valueName(l.Addr), size, &l.Location)
		g.valueTypes[l.Result] = l.Type
		return
	}
	op, err := g.loadOp(l.Type)
	if err != nil {
		g.reportError(err.Error(), &l.Location)
		return
	}
	qbeType, err := g.qbeType(l.Type)
	if err != nil {
		g.reportError(err.Error(), &l.Location)
		return
	}
	g.emitLine(fmt.Sprintf("%s =%s %s %s", g.valueName(l.Result), qbeType, op, g.valueName(l.Addr)))
	g.valueTypes[l.Result] = l.Type
}

func (g *Generator) emitPtrAdd(p *mir.PtrAdd) {
	if p == nil {
		return
	}
	base := g.valueName(p.Base)
	g.emitLine(fmt.Sprintf("%s =l add %s, %d", g.valueName(p.Result), base, p.Offset))
	g.valueTypes[p.Result] = types.NewReference(p.Elem)
}

func (g *Generator) emitPtrOffset(p *mir.PtrOffset) {
	if p == nil {
		return
	}
	base := g.valueName(p.Base)
	offsetType := g.valueTypes[p.Offset]
	offset := g.ensureLong(p.Offset, g.isSigned(offsetType))

	elemSize := g.layout.SizeOf(p.Elem)
	if elemSize < 0 {
		g.reportUnsupported("ptr_offset element", &p.Location)
		return
	}

	if elemSize != 1 {
		scaled := g.newTemp()
		g.emitLine(fmt.Sprintf("%s =l mul %s, %d", scaled, offset, elemSize))
		offset = scaled
	}

	g.emitLine(fmt.Sprintf("%s =l add %s, %s", g.valueName(p.Result), base, offset))
	g.valueTypes[p.Result] = types.NewReference(p.Elem)
}

func (g *Generator) emitStore(s *mir.Store) {
	if s == nil {
		return
	}
	valType := g.valueTypes[s.Value]
	if valType == nil {
		g.reportUnsupported("store value type", &s.Location)
		return
	}
	g.storeValueToAddr(s.Value, valType, g.valueName(s.Addr), &s.Location)
}

func (g *Generator) emitCall(c *mir.Call) {
	if c == nil {
		return
	}

	args := make([]callArg, 0, len(c.Args))
	for _, arg := range c.Args {
		semType := g.valueTypes[arg]
		if semType == nil {
			g.reportUnsupported("call arg type", &c.Location)
			return
		}
		qbeSem := normalizeLargeValueType(semType)
		if g.needsByRefType(qbeSem) {
			qbeSem = types.NewReference(qbeSem)
		}
		qbeType, err := g.qbeType(qbeSem)
		if err != nil {
			g.reportError(err.Error(), &c.Location)
			return
		}
		args = append(args, callArg{name: g.valueName(arg), typ: qbeType, sem: semType})
	}

	target, args, err := g.resolveCallTarget(c.Target, args)
	if err != nil {
		g.reportError(err.Error(), &c.Location)
		return
	}

	if len(args) == 1 && isEnumType(args[0].sem) && isPrintFunc(target) {
		table, count, err := g.enumStringTable(args[0].sem)
		if err != nil {
			g.reportError(err.Error(), &c.Location)
			return
		}
		tmp := g.newTemp()
		g.emitLine(fmt.Sprintf("%s =l call $ferret_enum_to_string(l %s, w %d, w %s)", tmp, table, count, args[0].name))
		args[0] = callArg{name: tmp, typ: "l", sem: types.TypeString}
	}

	if optType, ok := g.optionalType(c.Type); ok {
		size := g.layout.SizeOf(optType)
		if size <= 0 {
			g.reportUnsupported("optional call return", &c.Location)
			return
		}
		align := g.layout.AlignOf(optType)
		outName := g.stackAlloc(size, align)
		args = append([]callArg{{name: outName, typ: "l", sem: types.NewReference(c.Type)}}, args...)
		var argParts []string
		for _, arg := range args {
			argParts = append(argParts, fmt.Sprintf("%s %s", arg.typ, arg.name))
		}
		g.emitLine(fmt.Sprintf("call $%s(%s)", target, strings.Join(argParts, ", ")))
		if c.Result != mir.InvalidValue {
			op, err := g.loadOp(c.Type)
			if err != nil {
				g.reportError(err.Error(), &c.Location)
				return
			}
			qbeType, err := g.qbeType(c.Type)
			if err != nil {
				g.reportError(err.Error(), &c.Location)
				return
			}
			g.emitLine(fmt.Sprintf("%s =%s %s %s", g.valueName(c.Result), qbeType, op, outName))
			g.valueTypes[c.Result] = c.Type
		}
		return
	}
	if resType, ok := g.resultType(c.Type); ok {
		size := g.layout.SizeOf(resType)
		if size <= 0 {
			g.reportUnsupported("result call return", &c.Location)
			return
		}
		align := g.layout.AlignOf(resType)
		outName := ""
		if c.Result != mir.InvalidValue {
			outName = g.valueName(c.Result)
			g.emitLine(fmt.Sprintf("%s =l %s %d", outName, g.allocOp(align), size))
			g.valueTypes[c.Result] = c.Type
		} else {
			outName = g.stackAlloc(size, align)
		}
		args = append([]callArg{{name: outName, typ: "l", sem: types.NewReference(c.Type)}}, args...)
		var argParts []string
		for _, arg := range args {
			argParts = append(argParts, fmt.Sprintf("%s %s", arg.typ, arg.name))
		}
		g.emitLine(fmt.Sprintf("call $%s(%s)", target, strings.Join(argParts, ", ")))
		return
	}

	var argParts []string
	for _, arg := range args {
		argParts = append(argParts, fmt.Sprintf("%s %s", arg.typ, arg.name))
	}

	result := ""
	if c.Result != mir.InvalidValue {
		retType, err := g.qbeType(c.Type)
		if err != nil {
			g.reportError(err.Error(), &c.Location)
			return
		}
		result = fmt.Sprintf("%s =%s ", g.valueName(c.Result), retType)
		g.valueTypes[c.Result] = c.Type
	}

	g.emitLine(fmt.Sprintf("%scall $%s(%s)", result, target, strings.Join(argParts, ", ")))

	// For bool return types, mask to 1 byte to handle C ABI differences
	// C's bool is 1 byte, but QBE uses word (4 bytes) - upper bytes may contain garbage
	if c.Result != mir.InvalidValue && c.Type != nil && c.Type.Equals(types.TypeBool) {
		resultName := g.valueName(c.Result)
		masked := g.newTemp()
		g.emitLine(fmt.Sprintf("%s =w and %s, 1", masked, resultName))
		// Replace the result value with the masked one
		g.emitLine(fmt.Sprintf("%s =w copy %s", resultName, masked))
	}
}

func (g *Generator) emitCallIndirect(c *mir.CallIndirect) {
	if c == nil {
		return
	}

	args := make([]callArg, 0, len(c.Args))
	for _, arg := range c.Args {
		semType := g.valueTypes[arg]
		if semType == nil {
			g.reportUnsupported("call_indirect arg type", &c.Location)
			return
		}
		qbeSem := normalizeLargeValueType(semType)
		if g.needsByRefType(qbeSem) {
			qbeSem = types.NewReference(qbeSem)
		}
		qbeType, err := g.qbeType(qbeSem)
		if err != nil {
			g.reportError(err.Error(), &c.Location)
			return
		}
		args = append(args, callArg{name: g.valueName(arg), typ: qbeType, sem: semType})
	}

	fnPtr := g.newTemp()
	g.emitLine(fmt.Sprintf("%s =l loadl %s", fnPtr, g.valueName(c.Callee)))

	if optType, ok := g.optionalType(c.Type); ok {
		size := g.layout.SizeOf(optType)
		if size <= 0 {
			g.reportUnsupported("optional call_indirect return", &c.Location)
			return
		}
		align := g.layout.AlignOf(optType)
		outName := g.stackAlloc(size, align)
		args = append([]callArg{{name: outName, typ: "l", sem: types.NewReference(c.Type)}}, args...)
		argParts := []string{}
		for _, arg := range args {
			argParts = append(argParts, fmt.Sprintf("%s %s", arg.typ, arg.name))
		}
		g.emitLine(fmt.Sprintf("call %s(%s)", fnPtr, strings.Join(argParts, ", ")))
		if c.Result != mir.InvalidValue {
			op, err := g.loadOp(c.Type)
			if err != nil {
				g.reportError(err.Error(), &c.Location)
				return
			}
			qbeType, err := g.qbeType(c.Type)
			if err != nil {
				g.reportError(err.Error(), &c.Location)
				return
			}
			g.emitLine(fmt.Sprintf("%s =%s %s %s", g.valueName(c.Result), qbeType, op, outName))
			g.valueTypes[c.Result] = c.Type
		}
		return
	}
	if resType, ok := g.resultType(c.Type); ok {
		size := g.layout.SizeOf(resType)
		if size <= 0 {
			g.reportUnsupported("result call_indirect return", &c.Location)
			return
		}
		align := g.layout.AlignOf(resType)
		outName := ""
		if c.Result != mir.InvalidValue {
			outName = g.valueName(c.Result)
			g.emitLine(fmt.Sprintf("%s =l %s %d", outName, g.allocOp(align), size))
			g.valueTypes[c.Result] = c.Type
		} else {
			outName = g.stackAlloc(size, align)
		}
		args = append([]callArg{{name: outName, typ: "l", sem: types.NewReference(c.Type)}}, args...)
		argParts := []string{}
		for _, arg := range args {
			argParts = append(argParts, fmt.Sprintf("%s %s", arg.typ, arg.name))
		}
		g.emitLine(fmt.Sprintf("call %s(%s)", fnPtr, strings.Join(argParts, ", ")))
		return
	}

	argParts := []string{}
	for _, arg := range args {
		argParts = append(argParts, fmt.Sprintf("%s %s", arg.typ, arg.name))
	}

	result := ""
	if c.Result != mir.InvalidValue {
		retType, err := g.qbeType(c.Type)
		if err != nil {
			g.reportError(err.Error(), &c.Location)
			return
		}
		result = fmt.Sprintf("%s =%s ", g.valueName(c.Result), retType)
		g.valueTypes[c.Result] = c.Type
	}

	g.emitLine(fmt.Sprintf("%scall %s(%s)", result, fnPtr, strings.Join(argParts, ", ")))
}

func (g *Generator) emitMapGet(m *mir.MapGet) {
	if m == nil {
		return
	}
	mapType, ok := g.mapTypeOf(m.Map)
	if !ok || mapType == nil {
		g.reportUnsupported("map_get map", &m.Location)
		return
	}

	keyPtr := g.valueAddr(m.Key, mapType.Key, &m.Location)
	if keyPtr == "" {
		return
	}

	// Check if return type is optional or direct value
	optType, isOptional := g.optionalType(m.Type)
	if isOptional && optType != nil {
		// Optional return type: use ferret_map_get_optional_out
		payloadSize := g.layout.OptionalPayloadSize(optType)
		if payloadSize <= 0 {
			g.reportUnsupported("map_get optional payload size", &m.Location)
			return
		}
		sizeTemp := g.newTemp()
		g.emitLine(fmt.Sprintf("%s =l copy %d", sizeTemp, payloadSize))
		g.emitLine(fmt.Sprintf("%s =l call $ferret_alloc(l %s)", g.valueName(m.Result), sizeTemp))
		g.emitLine(fmt.Sprintf("call $ferret_map_get_optional_out(l %s, l %s, l %s)",
			g.valueName(m.Map), keyPtr, g.valueName(m.Result)))
		g.valueTypes[m.Result] = m.Type
	} else {
		// Direct value type: call ferret_map_get and panic if NULL
		valueType := m.Type
		valueSize := g.layout.SizeOf(valueType)
		if valueSize <= 0 {
			g.reportUnsupported("map_get value size", &m.Location)
			return
		}
		byRef := g.needsByRefType(valueType)
		if _, ok := types.UnwrapType(valueType).(*types.ReferenceType); ok {
			byRef = true
		}
		valuePtr := g.newTemp()
		if byRef {
			valuePtr = g.valueName(m.Result)
		}
		g.emitLine(fmt.Sprintf("%s =l call $ferret_map_get(l %s, l %s)", valuePtr, g.valueName(m.Map), keyPtr))

		// Check for NULL and panic if key not found
		nullCheck := g.newTemp()
		g.emitLine(fmt.Sprintf("%s =w ceql %s, 0", nullCheck, valuePtr))
		panicLabel := fmt.Sprintf("map_get_panic_%d", g.labelID)
		g.labelID++
		okLabel := fmt.Sprintf("map_get_ok_%d", g.labelID)
		g.labelID++
		g.emitLine(fmt.Sprintf("jnz %s, @%s, @%s", nullCheck, panicLabel, okLabel))
		g.emitLine(fmt.Sprintf("@%s", panicLabel))
		msgSym := g.stringSymbol("key not found in map")
		g.emitLine(fmt.Sprintf("call $ferret_global_panic(l %s)", msgSym))
		g.emitLine("ret") // Unreachable after panic, but QBE needs it
		g.emitLine(fmt.Sprintf("@%s", okLabel))

		if byRef {
			g.valueTypes[m.Result] = valueType
			return
		}

		// Load the value from the pointer
		loadOp, err := g.loadOp(valueType)
		if err != nil {
			g.reportError(err.Error(), &m.Location)
			return
		}
		qbeType, err := g.qbeType(valueType)
		if err != nil {
			g.reportError(err.Error(), &m.Location)
			return
		}
		loadResult := g.valueName(m.Result)
		g.emitLine(fmt.Sprintf("%s =%s %s %s", loadResult, qbeType, loadOp, valuePtr))
		g.valueTypes[m.Result] = valueType
	}
}

func (g *Generator) emitArrayGet(a *mir.ArrayGet) {
	if a == nil {
		return
	}
	arrType, ok := g.arrayTypeOf(a.Array)
	if !ok || arrType == nil {
		g.reportUnsupported("array_get array", &a.Location)
		return
	}

	elemType := a.Type
	elemSize := g.layout.SizeOf(elemType)
	if elemSize <= 0 {
		g.reportUnsupported("array_get element size", &a.Location)
		return
	}

	if arrType.Length >= 0 {
		// Fixed-size array: calculate offset and load directly
		// Bounds checking was done in MIR lowering
		arrPtr := g.valueName(a.Array)
		indexVal := g.valueName(a.Index)

		// Calculate offset: index * elemSize
		// Convert index to long for multiplication
		indexLong := g.newTemp()
		g.emitLine(fmt.Sprintf("%s =l extsw %s", indexLong, indexVal))
		offset := g.newTemp()
		g.emitLine(fmt.Sprintf("%s =l mul %s, %d", offset, indexLong, elemSize))

		// Add offset to array pointer
		elemPtr := g.newTemp()
		g.emitLine(fmt.Sprintf("%s =l add %s, %s", elemPtr, arrPtr, offset))

		// Load the element
		loadOp, err := g.loadOp(elemType)
		if err != nil {
			g.reportError(err.Error(), &a.Location)
			return
		}
		qbeType, err := g.qbeType(elemType)
		if err != nil {
			g.reportError(err.Error(), &a.Location)
			return
		}
		result := g.valueName(a.Result)
		g.emitLine(fmt.Sprintf("%s =%s %s %s", result, qbeType, loadOp, elemPtr))
		g.valueTypes[a.Result] = elemType
	} else {
		// Dynamic array: call ferret_array_get and check for NULL
		arrVal := g.valueName(a.Array)
		indexVal := g.valueName(a.Index)

		valuePtr := g.newTemp()
		g.emitLine(fmt.Sprintf("%s =l call $ferret_array_get(l %s, w %s)", valuePtr, arrVal, indexVal))

		// Check for NULL and panic if out of bounds
		nullCheck := g.newTemp()
		g.emitLine(fmt.Sprintf("%s =w ceql %s, 0", nullCheck, valuePtr))
		panicLabel := fmt.Sprintf("array_get_panic_%d", g.labelID)
		g.labelID++
		okLabel := fmt.Sprintf("array_get_ok_%d", g.labelID)
		g.labelID++
		g.emitLine(fmt.Sprintf("jnz %s, @%s, @%s", nullCheck, panicLabel, okLabel))
		g.emitLine(fmt.Sprintf("@%s", panicLabel))
		msgSym := g.stringSymbol("index out of bounds")
		g.emitLine(fmt.Sprintf("call $ferret_global_panic(l %s)", msgSym))
		g.emitLine("ret") // Unreachable after panic
		g.emitLine(fmt.Sprintf("@%s", okLabel))

		// Load the value from the pointer
		loadOp, err := g.loadOp(elemType)
		if err != nil {
			g.reportError(err.Error(), &a.Location)
			return
		}
		qbeType, err := g.qbeType(elemType)
		if err != nil {
			g.reportError(err.Error(), &a.Location)
			return
		}
		result := g.valueName(a.Result)
		g.emitLine(fmt.Sprintf("%s =%s %s %s", result, qbeType, loadOp, valuePtr))
		g.valueTypes[a.Result] = elemType
	}
}

func (g *Generator) emitArraySet(a *mir.ArraySet) {
	if a == nil {
		return
	}
	arrType, ok := g.arrayTypeOf(a.Array)
	if !ok || arrType == nil {
		g.reportUnsupported("array_set array", &a.Location)
		return
	}

	elemType := arrType.Element
	elemSize := g.layout.SizeOf(elemType)
	if elemSize <= 0 {
		g.reportUnsupported("array_set element size", &a.Location)
		return
	}

	if arrType.Length >= 0 {
		// Fixed-size array: calculate offset and store directly
		// Bounds checking was done in MIR lowering
		arrPtr := g.valueName(a.Array)
		indexVal := g.valueName(a.Index)

		// Calculate offset: index * elemSize
		// Convert index to long for multiplication
		indexLong := g.newTemp()
		g.emitLine(fmt.Sprintf("%s =l extsw %s", indexLong, indexVal))
		offset := g.newTemp()
		g.emitLine(fmt.Sprintf("%s =l mul %s, %d", offset, indexLong, elemSize))

		// Add offset to array pointer
		elemPtr := g.newTemp()
		g.emitLine(fmt.Sprintf("%s =l add %s, %s", elemPtr, arrPtr, offset))

		g.storeValueToAddr(a.Value, elemType, elemPtr, &a.Location)
	} else {
		// Dynamic array: call ferret_array_set and check return value
		valuePtr := g.valueAddr(a.Value, elemType, &a.Location)
		if valuePtr == "" {
			return
		}
		arrVal := g.valueName(a.Array)
		indexVal := g.valueName(a.Index)

		result := g.newTemp()
		g.emitLine(fmt.Sprintf("%s =w call $ferret_array_set(l %s, w %s, l %s)", result, arrVal, indexVal, valuePtr))

		// Check return value and panic if false (out of bounds)
		zeroCheck := g.newTemp()
		g.emitLine(fmt.Sprintf("%s =w ceqw %s, 0", zeroCheck, result))
		panicLabel := fmt.Sprintf("array_set_panic_%d", g.labelID)
		g.labelID++
		okLabel := fmt.Sprintf("array_set_ok_%d", g.labelID)
		g.labelID++
		g.emitLine(fmt.Sprintf("jnz %s, @%s, @%s", zeroCheck, panicLabel, okLabel))
		g.emitLine(fmt.Sprintf("@%s", panicLabel))
		msgSym := g.stringSymbol("index out of bounds")
		g.emitLine(fmt.Sprintf("call $ferret_global_panic(l %s)", msgSym))
		g.emitLine("ret") // Unreachable after panic
		g.emitLine(fmt.Sprintf("@%s", okLabel))
	}
}

func (g *Generator) emitMapSet(m *mir.MapSet) {
	if m == nil {
		return
	}
	mapType, ok := g.mapTypeOf(m.Map)
	if !ok || mapType == nil {
		g.reportUnsupported("map_set map", &m.Location)
		return
	}
	keyPtr := g.valueAddr(m.Key, mapType.Key, &m.Location)
	if keyPtr == "" {
		return
	}
	valPtr := g.valueAddr(m.Value, mapType.Value, &m.Location)
	if valPtr == "" {
		return
	}
	g.emitLine(fmt.Sprintf("call $ferret_map_set(l %s, l %s, l %s)",
		g.valueName(m.Map), keyPtr, valPtr))
}

func (g *Generator) emitOptionalNone(o *mir.OptionalNone) {
	if o == nil {
		return
	}
	optType, ok := g.optionalType(o.Type)
	if !ok || optType == nil {
		g.reportUnsupported("optional_none", &o.Location)
		return
	}
	payloadSize := g.layout.OptionalPayloadSize(optType)
	if payloadSize <= 0 {
		g.reportUnsupported("optional_none payload size", &o.Location)
		return
	}
	valSize := g.layout.SizeOf(optType.Inner)
	if valSize < 0 {
		g.reportUnsupported("optional_none inner size", &o.Location)
		return
	}

	sizeTemp := g.newTemp()
	g.emitLine(fmt.Sprintf("%s =l copy %d", sizeTemp, payloadSize))
	g.emitLine(fmt.Sprintf("%s =l call $ferret_alloc(l %s)", g.valueName(o.Result), sizeTemp))

	flagPtr := g.newTemp()
	g.emitLine(fmt.Sprintf("%s =l add %s, %d", flagPtr, g.valueName(o.Result), valSize))
	g.emitLine(fmt.Sprintf("storeb 0, %s", flagPtr))
	g.valueTypes[o.Result] = o.Type
}

func (g *Generator) emitOptionalSome(o *mir.OptionalSome) {
	if o == nil {
		return
	}
	optType, ok := g.optionalType(o.Type)
	if !ok || optType == nil {
		g.reportUnsupported("optional_some", &o.Location)
		return
	}
	payloadSize := g.layout.OptionalPayloadSize(optType)
	if payloadSize <= 0 {
		g.reportUnsupported("optional_some payload size", &o.Location)
		return
	}
	valSize := g.layout.SizeOf(optType.Inner)
	if valSize < 0 {
		g.reportUnsupported("optional_some inner size", &o.Location)
		return
	}

	sizeTemp := g.newTemp()
	g.emitLine(fmt.Sprintf("%s =l copy %d", sizeTemp, payloadSize))
	g.emitLine(fmt.Sprintf("%s =l call $ferret_alloc(l %s)", g.valueName(o.Result), sizeTemp))

	g.storeValueToAddr(o.Value, optType.Inner, g.valueName(o.Result), &o.Location)

	flagPtr := g.newTemp()
	g.emitLine(fmt.Sprintf("%s =l add %s, %d", flagPtr, g.valueName(o.Result), valSize))
	g.emitLine(fmt.Sprintf("storeb 1, %s", flagPtr))
	g.valueTypes[o.Result] = o.Type
}

func (g *Generator) emitOptionalIsSome(o *mir.OptionalIsSome) {
	if o == nil {
		return
	}
	optType, ok := g.optionalType(g.valueTypes[o.Value])
	if !ok || optType == nil {
		g.reportUnsupported("optional_is_some", &o.Location)
		return
	}
	valSize := g.layout.SizeOf(optType.Inner)
	if valSize < 0 {
		g.reportUnsupported("optional_is_some inner size", &o.Location)
		return
	}
	flagPtr := g.newTemp()
	g.emitLine(fmt.Sprintf("%s =l add %s, %d", flagPtr, g.valueName(o.Value), valSize))
	g.emitLine(fmt.Sprintf("%s =w loadub %s", g.valueName(o.Result), flagPtr))
	g.valueTypes[o.Result] = types.TypeBool
}

func (g *Generator) emitOptionalUnwrap(o *mir.OptionalUnwrap) {
	if o == nil {
		return
	}
	optType, ok := g.optionalType(g.valueTypes[o.Value])
	if !ok || optType == nil {
		g.reportUnsupported("optional_unwrap", &o.Location)
		return
	}
	inner := optType.Inner
	valSize := g.layout.SizeOf(inner)
	if valSize < 0 {
		g.reportUnsupported("optional_unwrap inner size", &o.Location)
		return
	}
	byRef := g.needsByRefType(inner)

	if o.HasDefault {
		outAlign := g.layout.AlignOf(inner)
		outAddr := ""
		if byRef {
			outAddr = g.valueName(o.Result)
			g.emitLine(fmt.Sprintf("%s =l %s %d", outAddr, g.allocOp(outAlign), valSize))
		} else {
			outAddr = g.stackAlloc(valSize, outAlign)
		}

		defaultPtr := ""
		if byRef {
			defaultPtr = g.valueName(o.Default)
		} else {
			defaultPtr = g.stackAlloc(valSize, outAlign)
			g.storeValueToAddr(o.Default, inner, defaultPtr, &o.Location)
		}

		sizeTemp := g.newTemp()
		g.emitLine(fmt.Sprintf("%s =l copy %d", sizeTemp, valSize))
		g.emitLine(fmt.Sprintf("call $ferret_optional_unwrap_or(l %s, l %s, l %s, l %s)",
			g.valueName(o.Value), defaultPtr, outAddr, sizeTemp))

		if byRef {
			g.valueTypes[o.Result] = types.NewReference(inner)
			return
		}
		op, err := g.loadOp(inner)
		if err != nil {
			g.reportError(err.Error(), &o.Location)
			return
		}
		qbeType, err := g.qbeType(inner)
		if err != nil {
			g.reportError(err.Error(), &o.Location)
			return
		}
		g.emitLine(fmt.Sprintf("%s =%s %s %s", g.valueName(o.Result), qbeType, op, outAddr))
		g.valueTypes[o.Result] = inner
		return
	}

	if byRef {
		align := g.layout.AlignOf(inner)
		g.emitLine(fmt.Sprintf("%s =l %s %d", g.valueName(o.Result), g.allocOp(align), valSize))
		g.emitMemcpy(g.valueName(o.Result), g.valueName(o.Value), valSize, &o.Location)
		g.valueTypes[o.Result] = types.NewReference(inner)
		return
	}

	op, err := g.loadOp(inner)
	if err != nil {
		g.reportError(err.Error(), &o.Location)
		return
	}
	qbeType, err := g.qbeType(inner)
	if err != nil {
		g.reportError(err.Error(), &o.Location)
		return
	}
	g.emitLine(fmt.Sprintf("%s =%s %s %s", g.valueName(o.Result), qbeType, op, g.valueName(o.Value)))
	g.valueTypes[o.Result] = inner
}

// emitUnionVariantCheck checks if a union value holds a specific variant.
// Union layout: [4-byte tag at offset 0][variant data at offset 4]
func (g *Generator) emitUnionVariantCheck(u *mir.UnionVariantCheck) {
	if u == nil {
		return
	}

	// Load the tag from offset 0 of the union
	tagPtr := g.newTemp()
	g.emitLine(fmt.Sprintf("%s =l add %s, 0", tagPtr, g.valueName(u.Value)))
	tagValue := g.newTemp()
	g.emitLine(fmt.Sprintf("%s =w loadsw %s", tagValue, tagPtr))

	// Compare with expected variant index
	g.emitLine(fmt.Sprintf("%s =w ceqw %s, %d", g.valueName(u.Result), tagValue, u.VariantIndex))
	g.valueTypes[u.Result] = types.TypeBool
}

// emitUnionExtract extracts the inner value from a union.
// Union layout: [4-byte tag at offset 0][variant data at offset 4]
func (g *Generator) emitUnionExtract(u *mir.UnionExtract) {
	if u == nil {
		return
	}

	variantType := u.Type
	if variantType == nil {
		g.reportUnsupported("union_extract nil type", &u.Location)
		return
	}

	variantSize := g.layout.SizeOf(variantType)
	if variantSize < 0 {
		g.reportUnsupported("union_extract variant size", &u.Location)
		return
	}

	// Calculate pointer to variant data (at offset 4 after the tag)
	dataPtr := g.newTemp()
	g.emitLine(fmt.Sprintf("%s =l add %s, 4", dataPtr, g.valueName(u.Value)))

	// Determine if we need to return by reference or by value
	byRef := g.needsByRefType(variantType)
	if !byRef {
		if _, ok := g.optionalType(variantType); ok {
			byRef = true
		} else if _, ok := g.resultType(variantType); ok {
			byRef = true
		} else if _, ok := variantType.(*types.UnionType); ok {
			byRef = true
		}
	}

	if byRef {
		// For reference types, allocate space and copy
		align := g.layout.AlignOf(variantType)
		g.emitLine(fmt.Sprintf("%s =l %s %d", g.valueName(u.Result), g.allocOp(align), variantSize))
		g.emitMemcpy(g.valueName(u.Result), dataPtr, variantSize, &u.Location)
		g.valueTypes[u.Result] = types.NewReference(variantType)
		return
	}

	// For value types, load directly
	op, err := g.loadOp(variantType)
	if err != nil {
		g.reportError(err.Error(), &u.Location)
		return
	}
	qbeType, err := g.qbeType(variantType)
	if err != nil {
		g.reportError(err.Error(), &u.Location)
		return
	}
	g.emitLine(fmt.Sprintf("%s =%s %s %s", g.valueName(u.Result), qbeType, op, dataPtr))
	g.valueTypes[u.Result] = variantType
}

func (g *Generator) emitResultOk(r *mir.ResultOk) {
	if r == nil {
		return
	}
	resType, ok := g.resultType(r.Type)
	if !ok || resType == nil {
		g.reportUnsupported("result_ok", &r.Location)
		return
	}
	resSize := g.layout.SizeOf(resType)
	if resSize <= 0 {
		g.reportUnsupported("result_ok size", &r.Location)
		return
	}
	tagOffset, ok := g.resultTagOffset(resType, &r.Location)
	if !ok {
		return
	}
	align := g.layout.AlignOf(resType)
	g.emitLine(fmt.Sprintf("%s =l %s %d", g.valueName(r.Result), g.allocOp(align), resSize))
	g.storeValueToAddr(r.Value, resType.Ok, g.valueName(r.Result), &r.Location)
	flagPtr := g.newTemp()
	g.emitLine(fmt.Sprintf("%s =l add %s, %d", flagPtr, g.valueName(r.Result), tagOffset))
	g.emitLine(fmt.Sprintf("storeb 1, %s", flagPtr))
	g.valueTypes[r.Result] = r.Type
}

func (g *Generator) emitResultErr(r *mir.ResultErr) {
	if r == nil {
		return
	}
	resType, ok := g.resultType(r.Type)
	if !ok || resType == nil {
		g.reportUnsupported("result_err", &r.Location)
		return
	}
	resSize := g.layout.SizeOf(resType)
	if resSize <= 0 {
		g.reportUnsupported("result_err size", &r.Location)
		return
	}
	tagOffset, ok := g.resultTagOffset(resType, &r.Location)
	if !ok {
		return
	}
	align := g.layout.AlignOf(resType)
	g.emitLine(fmt.Sprintf("%s =l %s %d", g.valueName(r.Result), g.allocOp(align), resSize))
	g.storeValueToAddr(r.Value, resType.Err, g.valueName(r.Result), &r.Location)
	flagPtr := g.newTemp()
	g.emitLine(fmt.Sprintf("%s =l add %s, %d", flagPtr, g.valueName(r.Result), tagOffset))
	g.emitLine(fmt.Sprintf("storeb 0, %s", flagPtr))
	g.valueTypes[r.Result] = r.Type
}

func (g *Generator) emitResultIsOk(r *mir.ResultIsOk) {
	if r == nil {
		return
	}
	resType, ok := g.resultType(g.valueTypes[r.Value])
	if !ok || resType == nil {
		g.reportUnsupported("result_is_ok", &r.Location)
		return
	}
	tagOffset, ok := g.resultTagOffset(resType, &r.Location)
	if !ok {
		return
	}
	flagPtr := g.newTemp()
	g.emitLine(fmt.Sprintf("%s =l add %s, %d", flagPtr, g.valueName(r.Value), tagOffset))
	g.emitLine(fmt.Sprintf("%s =w loadub %s", g.valueName(r.Result), flagPtr))
	g.valueTypes[r.Result] = types.TypeBool
}

func (g *Generator) emitResultUnwrap(r *mir.ResultUnwrap) {
	if r == nil {
		return
	}
	if r.HasDefault {
		g.reportUnsupported("result_unwrap default", &r.Location)
		return
	}
	inner := r.Type
	if inner == nil {
		g.reportUnsupported("result_unwrap type", &r.Location)
		return
	}
	valSize := g.layout.SizeOf(inner)
	if valSize < 0 {
		g.reportUnsupported("result_unwrap inner size", &r.Location)
		return
	}

	byRef := g.needsByRefType(inner)

	if byRef {
		align := g.layout.AlignOf(inner)
		g.emitLine(fmt.Sprintf("%s =l %s %d", g.valueName(r.Result), g.allocOp(align), valSize))
		g.emitMemcpy(g.valueName(r.Result), g.valueName(r.Value), valSize, &r.Location)
		g.valueTypes[r.Result] = types.NewReference(inner)
		return
	}

	op, err := g.loadOp(inner)
	if err != nil {
		g.reportError(err.Error(), &r.Location)
		return
	}
	qbeType, err := g.qbeType(inner)
	if err != nil {
		g.reportError(err.Error(), &r.Location)
		return
	}
	g.emitLine(fmt.Sprintf("%s =%s %s %s", g.valueName(r.Result), qbeType, op, g.valueName(r.Value)))
	g.valueTypes[r.Result] = inner
}

func (g *Generator) emitPhi(p *mir.Phi) {
	if p == nil {
		return
	}
	phiType := normalizeLargeValueType(p.Type)
	qbeType, err := g.qbeType(phiType)
	if err != nil {
		g.reportError(err.Error(), &p.Location)
		return
	}

	parts := make([]string, 0, len(p.Incoming))
	for _, in := range p.Incoming {
		parts = append(parts, fmt.Sprintf("%s %s", g.blockName(in.Pred), g.valueName(in.Value)))
	}

	g.emitLine(fmt.Sprintf("%s =%s phi %s", g.valueName(p.Result), qbeType, strings.Join(parts, ", ")))
	g.valueTypes[p.Result] = phiType
}

func (g *Generator) emitLine(line string) {
	g.buf.WriteString("\t")
	g.buf.WriteString(line)
	g.buf.WriteString("\n")
}

func (g *Generator) valueName(id mir.ValueID) string {
	if id == mir.InvalidValue {
		return "%t0"
	}
	return fmt.Sprintf("%%t%d", id)
}

func (g *Generator) blockName(id mir.BlockID) string {
	if id == mir.InvalidBlock {
		return "@b0"
	}
	return fmt.Sprintf("@b%d", id)
}

func (g *Generator) newTemp() string {
	g.tempID++
	return fmt.Sprintf("%%q%d", g.tempID)
}

func (g *Generator) ensureLong(id mir.ValueID, signed bool) string {
	valType := g.valueTypes[id]
	if valType == nil {
		return g.valueName(id)
	}
	qbeType, err := g.qbeType(valType)
	if err != nil || qbeType == "l" {
		return g.valueName(id)
	}
	op := "extsw"
	if !signed {
		op = "extuw"
	}
	tmp := g.newTemp()
	g.emitLine(fmt.Sprintf("%s =l %s %s", tmp, op, g.valueName(id)))
	return tmp
}

func (g *Generator) constValue(typ types.SemType, value string) (string, error) {
	if typ == nil {
		return "", fmt.Errorf("qbe: const missing type")
	}
	typ = types.UnwrapType(typ)

	if prim, ok := typ.(*types.PrimitiveType); ok {
		switch prim.GetName() {
		case types.TYPE_STRING:
			return g.stringSymbol(value), nil
		case types.TYPE_CHAR:
			// Char literals are stored as the character itself, convert to Unicode code point
			runes := []rune(value)
			if len(runes) != 1 {
				return "", fmt.Errorf("qbe: invalid char literal %q", value)
			}
			return fmt.Sprintf("%d", int32(runes[0])), nil
		case types.TYPE_BYTE:
			// Byte literals are stored as the character itself, convert to byte value
			if len(value) != 1 {
				return "", fmt.Errorf("qbe: invalid byte literal %q", value)
			}
			return fmt.Sprintf("%d", byte(value[0])), nil
		case types.TYPE_BOOL:
			if value == "true" {
				return "1", nil
			}
			if value == "false" {
				return "0", nil
			}
			return value, nil
		case types.TYPE_F32:
			val, err := g.normalizeFloat(value)
			if err != nil {
				return "", err
			}
			return "s_" + val, nil
		case types.TYPE_F64:
			val, err := g.normalizeFloat(value)
			if err != nil {
				return "", err
			}
			return "d_" + val, nil
		default:
			val, err := g.normalizeInt(value)
			if err != nil {
				return "", err
			}
			return val, nil
		}
	}
	if _, ok := typ.(*types.EnumType); ok {
		val, err := g.normalizeInt(value)
		if err != nil {
			return "", err
		}
		return val, nil
	}
	if _, ok := typ.(*types.FunctionType); ok {
		name, err := g.resolveFuncSymbol(value)
		if err != nil {
			return "", err
		}
		return "$" + name, nil
	}

	if _, ok := typ.(*types.ReferenceType); ok {
		if strings.HasPrefix(value, "$") {
			return value, nil
		}
		val, err := g.normalizeInt(value)
		if err != nil {
			return "", err
		}
		return val, nil
	}

	return "", fmt.Errorf("qbe: unsupported const type %s", typ.String())
}

func (g *Generator) stringSymbol(value string) string {
	if sym, ok := g.stringLits[value]; ok {
		return sym
	}
	g.stringID++
	name := fmt.Sprintf("$str%d", g.stringID)
	g.stringLits[value] = name

	escaped := escapeString(value)
	align := g.layout.AlignOf(types.TypeU8)
	if align < 1 {
		align = 1
	}
	g.data.WriteString(fmt.Sprintf("data %s = align %d { b \"%s\", b 0 }\n", name, align, escaped))
	return name
}

func (g *Generator) normalizeInt(value string) (string, error) {
	num, err := numeric.NewNumericValue(value)
	if err != nil {
		return "", fmt.Errorf("qbe: invalid integer literal %q", value)
	}
	return num.String(), nil
}

func (g *Generator) normalizeFloat(value string) (string, error) {
	num, err := numeric.NewFloatValue(value)
	if err != nil {
		return "", fmt.Errorf("qbe: invalid float literal %q", value)
	}
	return num.String(), nil
}

func isPrintFunc(target string) bool {
	return target == "ferret_io_Print" || target == "ferret_io_Println"
}

func isEnumType(typ types.SemType) bool {
	if typ == nil {
		return false
	}
	typ = types.UnwrapType(typ)
	_, ok := typ.(*types.EnumType)
	return ok
}

func (g *Generator) enumStringTable(typ types.SemType) (string, int, error) {
	enumType, key, err := enumTypeKey(typ)
	if err != nil {
		return "", 0, err
	}
	if table, ok := g.enumTables[key]; ok {
		return table, g.enumCounts[key], nil
	}

	g.enumTableID++
	tableName := fmt.Sprintf("$enumtbl%d", g.enumTableID)

	entries := make([]string, 0, len(enumType.Variants))
	for _, variant := range enumType.Variants {
		entries = append(entries, fmt.Sprintf("l %s", g.stringSymbol(variant.Name)))
	}

	align := g.layout.PointerSize
	if align < 1 {
		align = 1
	}
	g.data.WriteString(fmt.Sprintf("data %s = align %d { %s }\n", tableName, align, strings.Join(entries, ", ")))
	g.enumTables[key] = tableName
	g.enumCounts[key] = len(enumType.Variants)
	return tableName, len(enumType.Variants), nil
}

func enumTypeKey(typ types.SemType) (*types.EnumType, string, error) {
	if typ == nil {
		return nil, "", fmt.Errorf("qbe: missing enum type")
	}

	if ref, ok := typ.(*types.ReferenceType); ok {
		typ = ref.Inner
	}

	if named, ok := typ.(*types.NamedType); ok {
		if enumType, ok := named.Underlying.(*types.EnumType); ok {
			key := named.Name
			if key == "" && enumType.ID != "" {
				key = enumType.ID
			}
			if key == "" {
				key = fmt.Sprintf("anon_%p", enumType)
			}
			return enumType, key, nil
		}
	}

	typ = types.UnwrapType(typ)
	if enumType, ok := typ.(*types.EnumType); ok {
		if enumType.ID != "" {
			return enumType, enumType.ID, nil
		}
		return enumType, fmt.Sprintf("anon_%p", enumType), nil
	}

	return nil, "", fmt.Errorf("qbe: expected enum type, got %s", typ.String())
}

func escapeString(s string) string {
	replacer := strings.NewReplacer(
		"\\", "\\\\",
		"\"", "\\\"",
		"\n", "\\n",
		"\r", "\\r",
		"\t", "\\t",
	)
	return replacer.Replace(s)
}

func (g *Generator) binaryOp(op tokens.TOKEN, typ types.SemType) (string, error) {
	switch op {
	case tokens.PLUS_TOKEN:
		return "add", nil
	case tokens.MINUS_TOKEN:
		return "sub", nil
	case tokens.MUL_TOKEN:
		return "mul", nil
	case tokens.DIV_TOKEN:
		if g.isUnsigned(typ) {
			return "udiv", nil
		}
		return "div", nil
	case tokens.MOD_TOKEN:
		if g.isUnsigned(typ) {
			return "urem", nil
		}
		return "rem", nil
	case tokens.BIT_AND_TOKEN, tokens.AND_TOKEN:
		return "and", nil
	case tokens.BIT_OR_TOKEN, tokens.OR_TOKEN:
		return "or", nil
	case tokens.BIT_XOR_TOKEN:
		return "xor", nil
	default:
		return "", fmt.Errorf("qbe: unsupported binary op %s", op)
	}
}

func (g *Generator) compareOp(op tokens.TOKEN, typ types.SemType) (string, error) {
	qbeType, err := g.qbeType(typ)
	if err != nil {
		return "", err
	}

	if g.isFloat(typ) {
		switch op {
		case tokens.DOUBLE_EQUAL_TOKEN:
			return "ceq" + qbeType, nil
		case tokens.NOT_EQUAL_TOKEN:
			return "cne" + qbeType, nil
		case tokens.LESS_TOKEN:
			return "clt" + qbeType, nil
		case tokens.LESS_EQUAL_TOKEN:
			return "cle" + qbeType, nil
		case tokens.GREATER_TOKEN:
			return "cgt" + qbeType, nil
		case tokens.GREATER_EQUAL_TOKEN:
			return "cge" + qbeType, nil
		default:
			return "", fmt.Errorf("qbe: unsupported float compare %s", op)
		}
	}

	signed := g.isSigned(typ)
	switch op {
	case tokens.DOUBLE_EQUAL_TOKEN:
		return "ceq" + qbeType, nil
	case tokens.NOT_EQUAL_TOKEN:
		return "cne" + qbeType, nil
	case tokens.LESS_TOKEN:
		if signed {
			return "cslt" + qbeType, nil
		}
		return "cult" + qbeType, nil
	case tokens.LESS_EQUAL_TOKEN:
		if signed {
			return "csle" + qbeType, nil
		}
		return "cule" + qbeType, nil
	case tokens.GREATER_TOKEN:
		if signed {
			return "csgt" + qbeType, nil
		}
		return "cugt" + qbeType, nil
	case tokens.GREATER_EQUAL_TOKEN:
		if signed {
			return "csge" + qbeType, nil
		}
		return "cuge" + qbeType, nil
	default:
		return "", fmt.Errorf("qbe: unsupported compare %s", op)
	}
}

func (g *Generator) loadOp(typ types.SemType) (string, error) {
	if typ.Equals(types.TypeUnknown) {
		// Temporary fix: assume word load for unknown
		return "loadw", nil
	}
	typ = types.UnwrapType(typ)
	if _, ok := typ.(*types.UnionType); ok {
		return "loadl", nil // unions as pointers
	}
	if prim, ok := typ.(*types.PrimitiveType); ok {
		switch prim.GetName() {
		case types.TYPE_I8:
			return "loadsb", nil
		case types.TYPE_U8, types.TYPE_BYTE, types.TYPE_BOOL:
			return "loadub", nil
		case types.TYPE_I16:
			return "loadsh", nil
		case types.TYPE_U16:
			return "loaduh", nil
		case types.TYPE_I32:
			return "loadw", nil
		case types.TYPE_U32, types.TYPE_CHAR:
			return "loaduw", nil
		case types.TYPE_I64, types.TYPE_U64:
			return "loadl", nil
		case types.TYPE_F32:
			return "loads", nil
		case types.TYPE_F64:
			return "loadd", nil
		case types.TYPE_STRING:
			return "loadl", nil
		}
	}
	if _, ok := typ.(*types.EnumType); ok {
		return "loadw", nil
	}
	if _, ok := typ.(*types.FunctionType); ok {
		return "loadl", nil
	}
	if arr, ok := typ.(*types.ArrayType); ok && arr.Length < 0 {
		return "loadl", nil
	}
	if _, ok := typ.(*types.MapType); ok {
		return "loadl", nil
	}
	if iface, ok := typ.(*types.InterfaceType); ok {
		if len(iface.Methods) == 0 {
			return "loadl", nil
		}
	}
	if named, ok := typ.(*types.NamedType); ok {
		if iface, ok := named.Underlying.(*types.InterfaceType); ok && len(iface.Methods) == 0 {
			return "loadl", nil
		}
	}
	if _, ok := typ.(*types.ReferenceType); ok {
		return "loadl", nil
	}
	if _, ok := typ.(*types.OptionalType); ok {
		return "loadl", nil
	}
	return "", fmt.Errorf("qbe: unsupported load type %s", typ.String())
}

func (g *Generator) storeOp(typ types.SemType) (string, error) {
	typ = types.UnwrapType(typ)
	if prim, ok := typ.(*types.PrimitiveType); ok {
		switch prim.GetName() {
		case types.TYPE_I8, types.TYPE_U8, types.TYPE_BYTE, types.TYPE_BOOL:
			return "storeb", nil
		case types.TYPE_I16, types.TYPE_U16:
			return "storeh", nil
		case types.TYPE_I32, types.TYPE_U32, types.TYPE_CHAR:
			return "storew", nil
		case types.TYPE_I64, types.TYPE_U64:
			return "storel", nil
		case types.TYPE_F32:
			return "stores", nil
		case types.TYPE_F64:
			return "stored", nil
		case types.TYPE_STRING:
			return "storel", nil
		}
	}
	if _, ok := typ.(*types.EnumType); ok {
		return "storew", nil
	}
	if _, ok := typ.(*types.FunctionType); ok {
		return "storel", nil
	}
	if arr, ok := typ.(*types.ArrayType); ok && arr.Length < 0 {
		return "storel", nil
	}
	if _, ok := typ.(*types.MapType); ok {
		return "storel", nil
	}
	if iface, ok := typ.(*types.InterfaceType); ok {
		if len(iface.Methods) == 0 {
			return "storel", nil
		}
	}
	if named, ok := typ.(*types.NamedType); ok {
		if iface, ok := named.Underlying.(*types.InterfaceType); ok && len(iface.Methods) == 0 {
			return "storel", nil
		}
	}
	if _, ok := typ.(*types.ReferenceType); ok {
		return "storel", nil
	}
	if _, ok := typ.(*types.OptionalType); ok {
		return "storel", nil
	}
	return "", fmt.Errorf("qbe: unsupported store type %s", typ.String())
}

func (g *Generator) qbeType(typ types.SemType) (string, error) {
	if typ == nil {
		return "", fmt.Errorf("qbe: missing type")
	}
	if typ.Equals(types.TypeUnknown) {
		// Temporary fix: assume word type for unknown
		return "w", nil
	}
	typ = types.UnwrapType(typ)

	if prim, ok := typ.(*types.PrimitiveType); ok {
		switch prim.GetName() {
		case types.TYPE_I8, types.TYPE_I16, types.TYPE_I32,
			types.TYPE_U8, types.TYPE_U16, types.TYPE_U32,
			types.TYPE_BYTE, types.TYPE_CHAR, types.TYPE_BOOL:
			return "w", nil
		case types.TYPE_I64, types.TYPE_U64:
			return "l", nil
		case types.TYPE_F32:
			return "s", nil
		case types.TYPE_F64:
			return "d", nil
		case types.TYPE_STRING:
			return "l", nil
		case types.TYPE_VOID, types.TYPE_NONE:
			return "", nil
		default:
			return "", fmt.Errorf("qbe: unsupported primitive %s", prim.GetName())
		}
	}

	switch typ := typ.(type) {
	case *types.ReferenceType:
		return "l", nil
	case *types.FunctionType:
		return "l", nil
	case *types.EnumType:
		return "w", nil
	case *types.ArrayType:
		arr := typ
		if arr.Length < 0 {
			return "l", nil
		}
		return "", fmt.Errorf("qbe: fixed array values not supported")
	case *types.MapType:
		return "l", nil
	case *types.InterfaceType:
		return "l", nil
	case *types.OptionalType:
		return "l", nil
	case *types.ResultType:
		return "l", nil
	case *types.UnionType:
		return "l", nil // Represent unions as pointers for now
	}

	return "", fmt.Errorf("qbe: unsupported type %s", typ.String())
}

func (g *Generator) qbeReturnType(name string, typ types.SemType) (string, bool) {
	if name == "main" && (typ == nil || typ.Equals(types.TypeVoid)) {
		return "w", true
	}
	if typ == nil || typ.Equals(types.TypeVoid) || typ.Equals(types.TypeNone) {
		return "", false
	}
	if isLargePrimitiveType(typ) {
		g.reportError("qbe: large return types must be lowered to out parameters", nil)
		return "", false
	}
	retType, err := g.qbeType(typ)
	if err != nil {
		g.reportError(err.Error(), nil)
		return "", false
	}
	return retType, false
}

func (g *Generator) resolveCallTarget(target string, args []callArg) (string, []callArg, error) {
	if strings.HasPrefix(target, "ferret_") {
		return target, args, nil
	}

	if strings.Contains(target, "::") {
		parts := strings.Split(target, "::")
		moduleAlias := strings.Join(parts[:len(parts)-1], "::")
		funcName := parts[len(parts)-1]
		if moduleAlias == "" || funcName == "" {
			return "", args, fmt.Errorf("qbe: invalid call target %q", target)
		}

		importPath := ""
		if g.mod != nil && g.mod.ImportAliasMap != nil {
			importPath = g.mod.ImportAliasMap[moduleAlias]
		}
		if importPath == "" {
			return "", args, fmt.Errorf("qbe: unknown module alias %q", moduleAlias)
		}

		if g.ctx != nil {
			if imported, ok := g.ctx.GetModule(importPath); ok && imported.ModuleScope != nil {
				if sym, ok := imported.ModuleScope.GetSymbol(funcName); ok && sym.IsNative && sym.NativeName != "" {
					return sym.NativeName, args, nil
				}
			}
		}

		return g.qbeFuncName(funcName, importPath), args, nil
	}

	if g.mod != nil && g.mod.ModuleScope != nil {
		if sym, ok := g.mod.ModuleScope.Lookup(target); ok {
			if sym.IsNative && sym.NativeName != "" {
				return sym.NativeName, args, nil
			}
			if g.ctx != nil && g.ctx.Universe != nil && sym.DeclaredScope == g.ctx.Universe && sym.Kind == symbols.SymbolFunction {
				return g.qbeFuncName(target, context_v2.GlobalModuleImport), args, nil
			}
		}
	}

	return g.qbeFuncName(target, g.moduleImportPath()), args, nil
}

func (g *Generator) resolveFuncSymbol(target string) (string, error) {
	if target == "" {
		return "", fmt.Errorf("qbe: invalid function symbol")
	}
	if strings.HasPrefix(target, "ferret_") {
		return target, nil
	}

	if strings.Contains(target, "::") {
		parts := strings.Split(target, "::")
		moduleAlias := strings.Join(parts[:len(parts)-1], "::")
		funcName := parts[len(parts)-1]
		if moduleAlias == "" || funcName == "" {
			return "", fmt.Errorf("qbe: invalid function symbol %q", target)
		}

		importPath := ""
		if g.mod != nil && g.mod.ImportAliasMap != nil {
			importPath = g.mod.ImportAliasMap[moduleAlias]
		}
		if importPath == "" {
			return "", fmt.Errorf("qbe: unknown module alias %q", moduleAlias)
		}

		if g.ctx != nil {
			if imported, ok := g.ctx.GetModule(importPath); ok && imported.ModuleScope != nil {
				if sym, ok := imported.ModuleScope.GetSymbol(funcName); ok && sym.IsNative && sym.NativeName != "" {
					return sym.NativeName, nil
				}
			}
		}

		return g.qbeFuncName(funcName, importPath), nil
	}

	if g.mod != nil && g.mod.ModuleScope != nil {
		if sym, ok := g.mod.ModuleScope.Lookup(target); ok {
			if sym.IsNative && sym.NativeName != "" {
				return sym.NativeName, nil
			}
			if g.ctx != nil && g.ctx.Universe != nil && sym.DeclaredScope == g.ctx.Universe && sym.Kind == symbols.SymbolFunction {
				return g.qbeFuncName(target, context_v2.GlobalModuleImport), nil
			}
		}
	}

	return g.qbeFuncName(target, g.moduleImportPath()), nil
}

func (g *Generator) qbeFuncName(name, importPath string) string {
	base := g.sanitizeName(name)
	if name != "main" && importPath != "" && importPath != g.entryModulePath() {
		prefix := g.sanitizeName(ustrings.ToIdentifier(importPath))
		return "ferret_" + prefix + "_" + base
	}
	return base
}

func (g *Generator) sanitizeName(name string) string {
	return strings.ReplaceAll(name, "::", "_")
}

func (g *Generator) moduleImportPath() string {
	if g.mod != nil && g.mod.ImportPath != "" {
		return g.mod.ImportPath
	}
	if g.mirMod != nil {
		return g.mirMod.ImportPath
	}
	return ""
}

func (g *Generator) entryModulePath() string {
	if g.ctx != nil {
		return g.ctx.EntryModule
	}
	return ""
}

func (g *Generator) reportError(message string, loc *source.Location) {
	if g.ctx == nil {
		return
	}
	g.ctx.ReportError(message, loc)
}

func (g *Generator) reportUnsupported(what string, loc *source.Location) {
	g.reportError(fmt.Sprintf("qbe: unsupported %s", what), loc)
}

func (g *Generator) isSigned(typ types.SemType) bool {
	typ = types.UnwrapType(typ)
	if _, ok := typ.(*types.EnumType); ok {
		return true
	}
	if prim, ok := typ.(*types.PrimitiveType); ok && prim.GetName() == types.TYPE_BOOL {
		return false
	}
	if prim, ok := typ.(*types.PrimitiveType); ok {
		return types.IsSigned(prim.GetName())
	}
	return true
}

func (g *Generator) isUnsigned(typ types.SemType) bool {
	typ = types.UnwrapType(typ)
	if _, ok := typ.(*types.EnumType); ok {
		return false
	}
	if prim, ok := typ.(*types.PrimitiveType); ok && prim.GetName() == types.TYPE_BOOL {
		return true
	}
	if prim, ok := typ.(*types.PrimitiveType); ok {
		return types.IsUnsigned(prim.GetName())
	}
	return false
}

func (g *Generator) isInteger(typ types.SemType) bool {
	typ = types.UnwrapType(typ)
	if _, ok := typ.(*types.EnumType); ok {
		return true
	}
	if prim, ok := typ.(*types.PrimitiveType); ok && prim.GetName() == types.TYPE_BOOL {
		return true
	}
	return types.IsInteger(typ)
}

func (g *Generator) isFloat(typ types.SemType) bool {
	return types.IsFloat(types.UnwrapType(typ))
}

func isLargePrimitiveType(typ types.SemType) bool {
	if typ == nil {
		return false
	}
	typ = types.UnwrapType(typ)
	prim, ok := typ.(*types.PrimitiveType)
	if !ok {
		return false
	}
	switch prim.GetName() {
	case types.TYPE_I128, types.TYPE_U128, types.TYPE_I256, types.TYPE_U256,
		types.TYPE_F128, types.TYPE_F256:
		return true
	default:
		return false
	}
}

func normalizeLargeValueType(typ types.SemType) types.SemType {
	if isLargePrimitiveType(typ) {
		return types.NewReference(typ)
	}
	return typ
}

func (g *Generator) optionalType(typ types.SemType) (*types.OptionalType, bool) {
	if typ == nil {
		return nil, false
	}
	typ = types.UnwrapType(typ)
	if ref, ok := typ.(*types.ReferenceType); ok {
		typ = types.UnwrapType(ref.Inner)
	}
	opt, ok := typ.(*types.OptionalType)
	return opt, ok
}

func (g *Generator) resultType(typ types.SemType) (*types.ResultType, bool) {
	if typ == nil {
		return nil, false
	}
	typ = types.UnwrapType(typ)
	if ref, ok := typ.(*types.ReferenceType); ok {
		typ = types.UnwrapType(ref.Inner)
	}
	res, ok := typ.(*types.ResultType)
	return res, ok
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func alignToSize(size, align int) int {
	if align <= 1 {
		return size
	}
	rem := size % align
	if rem == 0 {
		return size
	}
	return size + (align - rem)
}

func (g *Generator) resultTagOffset(res *types.ResultType, loc *source.Location) (int, bool) {
	if res == nil {
		return 0, false
	}
	okSize := g.layout.SizeOf(res.Ok)
	errSize := g.layout.SizeOf(res.Err)
	if okSize < 0 || errSize < 0 {
		g.reportUnsupported("result size", loc)
		return 0, false
	}
	okAlign := g.layout.AlignOf(res.Ok)
	errAlign := g.layout.AlignOf(res.Err)
	unionAlign := maxInt(okAlign, errAlign)
	if unionAlign < 1 {
		unionAlign = 1
	}
	unionSize := alignToSize(maxInt(okSize, errSize), unionAlign)
	return unionSize, true
}

func (g *Generator) mapTypeOf(id mir.ValueID) (*types.MapType, bool) {
	typ := g.valueTypes[id]
	if typ == nil {
		return nil, false
	}
	typ = types.UnwrapType(typ)
	if ref, ok := typ.(*types.ReferenceType); ok {
		typ = types.UnwrapType(ref.Inner)
	}
	if mapType, ok := typ.(*types.MapType); ok {
		return mapType, true
	}
	return nil, false
}

func (g *Generator) arrayTypeOf(id mir.ValueID) (*types.ArrayType, bool) {
	typ := g.valueTypes[id]
	if typ == nil {
		return nil, false
	}
	typ = types.UnwrapType(typ)
	if ref, ok := typ.(*types.ReferenceType); ok {
		typ = types.UnwrapType(ref.Inner)
	}
	if arrType, ok := typ.(*types.ArrayType); ok {
		return arrType, true
	}
	return nil, false
}

func (g *Generator) allocOp(align int) string {
	op := "alloc4"
	if align > 4 && align <= 8 {
		op = "alloc8"
	} else if align > 8 {
		op = "alloc16"
	}
	return op
}

func (g *Generator) stackAlloc(size, align int) string {
	name := g.newTemp()
	g.emitLine(fmt.Sprintf("%s =l %s %d", name, g.allocOp(align), size))
	return name
}

func (g *Generator) emitMemcpy(dst, src string, size int, loc *source.Location) {
	if size <= 0 {
		g.reportUnsupported("memcpy size", loc)
		return
	}
	sizeTemp := g.newTemp()
	g.emitLine(fmt.Sprintf("%s =l copy %d", sizeTemp, size))
	g.emitLine(fmt.Sprintf("call $ferret_memcpy(l %s, l %s, l %s)", dst, src, sizeTemp))
}

func (g *Generator) needsByRefType(typ types.SemType) bool {
	if typ == nil {
		return false
	}
	typ = types.UnwrapType(typ)
	if _, ok := typ.(*types.ReferenceType); ok {
		return false
	}
	if isLargePrimitiveType(typ) {
		return true
	}
	if _, ok := typ.(*types.StructType); ok {
		return true
	}
	if arr, ok := typ.(*types.ArrayType); ok && arr.Length >= 0 {
		return true
	}
	if _, ok := typ.(*types.InterfaceType); ok {
		return true
	}
	if named, ok := typ.(*types.NamedType); ok {
		if _, ok := named.Underlying.(*types.InterfaceType); ok {
			return true
		}
	}
	return false
}

func (g *Generator) storeValueToAddr(val mir.ValueID, typ types.SemType, addr string, loc *source.Location) {
	if typ == nil {
		g.reportUnsupported("store value type", loc)
		return
	}
	if resType, ok := g.resultType(typ); ok {
		size := g.layout.SizeOf(resType)
		if size <= 0 {
			g.reportUnsupported("store result size", loc)
			return
		}
		g.emitMemcpy(addr, g.valueName(val), size, loc)
		return
	}
	if g.needsByRefType(typ) {
		size := g.layout.SizeOf(typ)
		if size <= 0 {
			g.reportUnsupported("store byref size", loc)
			return
		}
		g.emitMemcpy(addr, g.valueName(val), size, loc)
		return
	}
	op, err := g.storeOp(typ)
	if err != nil {
		g.reportError(err.Error(), loc)
		return
	}
	g.emitLine(fmt.Sprintf("%s %s, %s", op, g.valueName(val), addr))
}

func (g *Generator) valueAddr(val mir.ValueID, typ types.SemType, loc *source.Location) string {
	if typ == nil {
		g.reportUnsupported("value address type", loc)
		return ""
	}
	if g.needsByRefType(typ) {
		return g.valueName(val)
	}
	if _, ok := g.resultType(typ); ok {
		return g.valueName(val)
	}
	size := g.layout.SizeOf(typ)
	if size <= 0 {
		g.reportUnsupported("value address size", loc)
		return ""
	}
	align := g.layout.AlignOf(typ)
	addr := g.stackAlloc(size, align)
	g.storeValueToAddr(val, typ, addr, loc)
	return addr
}

func (g *Generator) emitOptionalCopy(dst, src string, optType types.SemType, loc *source.Location) {
	op, err := g.storeOp(optType)
	if err != nil {
		g.reportError(err.Error(), loc)
		return
	}
	g.emitLine(fmt.Sprintf("%s %s, %s", op, src, dst))
}

func (g *Generator) emitResultCopy(dst, src string, resType types.SemType, loc *source.Location) {
	size := g.layout.SizeOf(resType)
	if size <= 0 {
		g.reportUnsupported("result copy size", loc)
		return
	}
	g.emitMemcpy(dst, src, size, loc)
}
