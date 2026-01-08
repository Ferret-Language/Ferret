#include "all.h"

/* This emitter mirrors amd64/emit.c but uses the Win64 callee-save set. */

#define CMP(X) \
	X(Ciule,      "be") \
	X(Ciult,      "b")  \
	X(Cisle,      "le") \
	X(Cislt,      "l")  \
	X(Cisgt,      "g")  \
	X(Cisge,      "ge") \
	X(Ciugt,      "a")  \
	X(Ciuge,      "ae") \
	X(Cieq,       "z")  \
	X(Cine,       "nz") \
	X(NCmpI+Cfle, "be") \
	X(NCmpI+Cflt, "b")  \
	X(NCmpI+Cfgt, "a")  \
	X(NCmpI+Cfge, "ae") \
	X(NCmpI+Cfeq, "z")  \
	X(NCmpI+Cfne, "nz") \
	X(NCmpI+Cfo,  "np") \
	X(NCmpI+Cfuo, "p")

enum {
	SLong = 0,
	SWord = 1,
	SShort = 2,
	SByte = 3,

	Ki = -1, /* matches Kw and Kl */
	Ka = -2, /* matches all classes */
};

static struct {
	short op;
	short cls;
	char *asm;
} omap_win64[] = {
	{ Oadd,    Ka, "+add%k %1, %=" },
	{ Osub,    Ka, "-sub%k %1, %=" },
	{ Oand,    Ki, "+and%k %1, %=" },
	{ Oor,     Ki, "+or%k %1, %=" },
	{ Oxor,    Ki, "+xor%k %1, %=" },
	{ Osar,    Ki, "-sar%k %B1, %=" },
	{ Oshr,    Ki, "-shr%k %B1, %=" },
	{ Oshl,    Ki, "-shl%k %B1, %=" },
	{ Omul,    Ki, "+imul%k %1, %=" },
	{ Omul,    Ks, "+mulss %1, %=" },
	{ Omul,    Kd, "+mulsd %1, %=" },
	{ Odiv,    Ka, "-div%k %1, %=" },
	{ Ostorel, Ka, "movq %L0, %M1" },
	{ Ostorew, Ka, "movl %W0, %M1" },
	{ Ostoreh, Ka, "movw %H0, %M1" },
	{ Ostoreb, Ka, "movb %B0, %M1" },
	{ Ostores, Ka, "movss %S0, %M1" },
	{ Ostored, Ka, "movsd %D0, %M1" },
	{ Oload,   Ka, "mov%k %M0, %=" },
	{ Oloadsw, Kl, "movslq %M0, %L=" },
	{ Oloadsw, Kw, "movl %M0, %W=" },
	{ Oloaduw, Ki, "movl %M0, %W=" },
	{ Oloadsh, Ki, "movsw%k %M0, %=" },
	{ Oloaduh, Ki, "movzw%k %M0, %=" },
	{ Oloadsb, Ki, "movsb%k %M0, %=" },
	{ Oloadub, Ki, "movzb%k %M0, %=" },
	{ Oextsw,  Kl, "movslq %W0, %L=" },
	{ Oextuw,  Kl, "movl %W0, %W=" },
	{ Oextsh,  Ki, "movsw%k %H0, %=" },
	{ Oextuh,  Ki, "movzw%k %H0, %=" },
	{ Oextsb,  Ki, "movsb%k %B0, %=" },
	{ Oextub,  Ki, "movzb%k %B0, %=" },

	{ Oexts,   Kd, "cvtss2sd %0, %=" },
	{ Otruncd, Ks, "cvttsd2ss %0, %=" },
	{ Ostosi,  Ki, "cvttss2si%k %0, %=" },
	{ Odtosi,  Ki, "cvttsd2si%k %0, %=" },
	{ Oswtof,  Ka, "cvtsi2%k %W0, %=" },
	{ Osltof,  Ka, "cvtsi2%k %L0, %=" },
	{ Ocast,   Ki, "movq %D0, %L=" },
	{ Ocast,   Ka, "movq %L0, %D=" },

	{ Oaddr,   Ki, "lea%k %M0, %=" },
	{ Oswap,   Ki, "xchg%k %0, %1" },
	{ Osign,   Kl, "cqto" },
	{ Osign,   Kw, "cltd" },
	{ Oxdiv,   Ki, "div%k %0" },
	{ Oxidiv,  Ki, "idiv%k %0" },
	{ Oxcmp,   Ks, "comiss %S0, %S1" },
	{ Oxcmp,   Kd, "comisd %D0, %D1" },
	{ Oxcmp,   Ki, "cmp%k %0, %1" },
	{ Oxtest,  Ki, "test%k %0, %1" },
#define X(c, s) \
	{ Oflag+c, Ki, "set" s " %B=\n\tmovzb%k %B=, %=" },
	CMP(X)
#undef X
	{ NOp, 0, 0 }
};

static char *rname_win64[][4] = {
	[RAX] = {"rax", "eax", "ax", "al"},
	[RBX] = {"rbx", "ebx", "bx", "bl"},
	[RCX] = {"rcx", "ecx", "cx", "cl"},
	[RDX] = {"rdx", "edx", "dx", "dl"},
	[RSI] = {"rsi", "esi", "si", "sil"},
	[RDI] = {"rdi", "edi", "di", "dil"},
	[RBP] = {"rbp", "ebp", "bp", "bpl"},
	[RSP] = {"rsp", "esp", "sp", "spl"},
	[R8 ] = {"r8" , "r8d", "r8w", "r8b"},
	[R9 ] = {"r9" , "r9d", "r9w", "r9b"},
	[R10] = {"r10", "r10d", "r10w", "r10b"},
	[R11] = {"r11", "r11d", "r11w", "r11b"},
	[R12] = {"r12", "r12d", "r12w", "r12b"},
	[R13] = {"r13", "r13d", "r13w", "r13b"},
	[R14] = {"r14", "r14d", "r14w", "r14b"},
	[R15] = {"r15", "r15d", "r15w", "r15b"},
};

static int
slot(int s, Fn *fn)
{
	struct { int i:29; } x;

	x.i = s;
	assert(x.i <= fn->slot);
	if (x.i < 0)
		return -4 * x.i;
	else
		return -4 * (fn->slot - x.i);
}

static char *
regtoa(int reg, int sz)
{
	static char buf[6];

	if (reg >= XMM0) {
		sprintf(buf, "xmm%d", reg-XMM0);
		return buf;
	} else
		return rname_win64[reg][sz];
}

static void emitins(Ins, Fn *, FILE *);
static void emitcon(Con *con, FILE *f);

static void
emitcopy(Ref r1, Ref r2, int k, Fn *fn, FILE *f)
{
	Ins icp;

	/* Windows PE+ fix: Symbol addresses (CAddr) cannot use immediate addressing
	 * due to R_X86_64_32S relocation limitations. Use LEA with RIP-relative. */
	if (rtype(r2) == RCon && fn->con[r2.val].type == CAddr) {
		/* Load symbol address using LEA: leaq symbol(%rip), temp */
		fprintf(f, "\tleaq ");
		emitcon(&fn->con[r2.val], f);
		fprintf(f, "(%%rip), %%r11\n");
		
		/* Now copy from temp register to destination */
		icp.op = Ocopy;
		icp.arg[0] = TMP(R11);
		icp.to = r1;
		icp.cls = k;
		emitins(icp, fn, f);
		return;
	}

	icp.op = Ocopy;
	icp.arg[0] = r2;
	icp.to = r1;
	icp.cls = k;
	emitins(icp, fn, f);
}

static void
emitcon(Con *con, FILE *f)
{
	char *p;

	switch (con->type) {
	case CAddr:
		p = con->local ? gasloc : gassym;
		fprintf(f, "%s%s", p, str(con->label));
		if (con->bits.i)
			fprintf(f, "%+"PRId64, con->bits.i);
		break;
	case CBits:
		fprintf(f, "%"PRId64, con->bits.i);
		break;
	default:
		die("unreachable");
	}
}

static Ref
getarg(char c, Ins *i)
{
	switch (c) {
	case '0':
		return i->arg[0];
	case '1':
		return i->arg[1];
	case '=':
		return i->to;
	default:
		die("invalid arg letter %c", c);
	}
}

static void
emitf(char *s, Ins *i, Fn *fn, FILE *f)
{
	static char clstoa[][3] = {"l", "q", "ss", "sd"};
	char c;
	int sz;
	Ref ref;
	Mem *m;
	Con off;

	switch (*s) {
	case '+':
		if (req(i->arg[1], i->to)) {
			ref = i->arg[0];
			i->arg[0] = i->arg[1];
			i->arg[1] = ref;
		}
		/* fall through */
	case '-':
		assert((!req(i->arg[1], i->to) || req(i->arg[0], i->to)) &&
			"cannot convert to 2-address");
		emitcopy(i->to, i->arg[0], i->cls, fn, f);
		s++;
		break;
	}

	fputc('\t', f);
Next:
	while ((c = *s++) != '%')
		if (!c) {
			fputc('\n', f);
			return;
		} else
			fputc(c, f);
	switch ((c = *s++)) {
	case '%':
		fputc('%', f);
		break;
	case 'k':
		fputs(clstoa[i->cls], f);
		break;
	case '0':
	case '1':
	case '=':
		sz = KWIDE(i->cls) ? SLong : SWord;
		s--;
		goto Ref;
	case 'D':
	case 'S':
		sz = SLong;
	Ref:
		c = *s++;
		ref = getarg(c, i);
		switch (rtype(ref)) {
		case RTmp:
			assert(isreg(ref));
			fprintf(f, "%%%s", regtoa(ref.val, sz));
			break;
		case RSlot:
			fprintf(f, "%d(%%rbp)", slot(ref.val, fn));
			break;
		case RMem:
		Mem:
			m = &fn->mem[ref.val];
			if (rtype(m->base) == RSlot) {
				off.type = CBits;
				off.bits.i = slot(m->base.val, fn);
				addcon(&m->offset, &off);
				m->base = TMP(RBP);
			}
			if (m->offset.type != CUndef)
				emitcon(&m->offset, f);
			fputc('(', f);
			if (req(m->base, R))
				fprintf(f, "%%rip");
			else
				fprintf(f, "%%%s", regtoa(m->base.val, SLong));
			if (!req(m->index, R))
				fprintf(f, ", %%%s, %d",
					regtoa(m->index.val, SLong),
					m->scale
				);
			fputc(')', f);
			break;
		case RCon:
			fputc('$', f);
			emitcon(&fn->con[ref.val], f);
			break;
		default:
			die("unreachable");
		}
		break;
	case 'L':
		sz = SLong;
		goto Ref;
	case 'W':
		sz = SWord;
		goto Ref;
	case 'H':
		sz = SShort;
		goto Ref;
	case 'B':
		sz = SByte;
		goto Ref;
	case 'M':
		c = *s++;
		ref = getarg(c, i);
		switch (rtype(ref)) {
		case RMem:
			goto Mem;
		case RSlot:
			fprintf(f, "%d(%%rbp)", slot(ref.val, fn));
			break;
		case RCon:
			emitcon(&fn->con[ref.val], f);
			fprintf(f, "(%%rip)");
			break;
		case RTmp:
			assert(isreg(ref));
			fprintf(f, "(%%%s)", regtoa(ref.val, SLong));
			break;
		default:
			die("unreachable");
		}
		break;
	default:
		die("invalid format specifier %%%c", c);
	}
	goto Next;
}

static void *negmask[4] = {
	[Ks] = (uint32_t[4]){ 0x80000000 },
	[Kd] = (uint64_t[2]){ 0x8000000000000000 },
};

static void
emitins(Ins i, Fn *fn, FILE *f)
{
	Ref r;
	int64_t val;
	int o;

	switch (i.op) {
	default:
	Table:
		for (o=0;; o++) {
			if (omap_win64[o].op == NOp)
				die("no match for %s(%d)",
					optab[i.op].name, "wlsd"[i.cls]);
			if (omap_win64[o].op == i.op)
			if (omap_win64[o].cls == i.cls
			|| (omap_win64[o].cls == Ki && KBASE(i.cls) == 0)
			|| (omap_win64[o].cls == Ka))
				break;
		}
		emitf(omap_win64[o].asm, &i, fn, f);
		break;
	case Onop:
		break;
	case Omul:
		if (rtype(i.arg[1]) == RCon) {
			r = i.arg[0];
			i.arg[0] = i.arg[1];
			i.arg[1] = r;
		}
		if (KBASE(i.cls) == 0
		&& rtype(i.arg[0]) == RCon
		&& rtype(i.arg[1]) == RTmp) {
			emitf("imul%k %0, %1, %=", &i, fn, f);
			break;
		}
		goto Table;
	case Ostorel:
	case Ostorew:
	case Ostoreh:
	case Ostoreb:
	case Ostores:
	case Ostored:
		/* Windows PE+ fix: Cannot store symbol addresses directly using
		 * immediate addressing. Load address into temp register first. */
		if (rtype(i.arg[0]) == RCon && fn->con[i.arg[0].val].type == CAddr) {
			/* Load symbol address: leaq symbol(%rip), %r11 */
			fprintf(f, "\tleaq ");
			emitcon(&fn->con[i.arg[0].val], f);
			fprintf(f, "(%%rip), %%r11\n");
			/* Now do the store with the temp register */
			i.arg[0] = TMP(R11);
		}
		goto Table;
	case Oxcmp:
	case Oxtest:
		/* Windows PE+ fix: Cannot use symbol addresses as immediate in cmp/test.
		 * Load into temp register if either operand is a symbol address. */
		if (rtype(i.arg[0]) == RCon && fn->con[i.arg[0].val].type == CAddr) {
			fprintf(f, "\tleaq ");
			emitcon(&fn->con[i.arg[0].val], f);
			fprintf(f, "(%%rip), %%r11\n");
			i.arg[0] = TMP(R11);
		}
		if (rtype(i.arg[1]) == RCon && fn->con[i.arg[1].val].type == CAddr) {
			fprintf(f, "\tleaq ");
			emitcon(&fn->con[i.arg[1].val], f);
			fprintf(f, "(%%rip), %%r10\n");
			i.arg[1] = TMP(R10);
		}
		goto Table;
	case Osub:
		if (req(i.to, i.arg[1])) {
			if (KBASE(i.cls) == 0)
				emitf("neg%k %=", &i, fn, f);
			else
				fprintf(f,
					"\txorp%c %sfp%d(%%rip), %%%s\n",
					"xxsd"[i.cls],
					gasloc,
					gasstash(negmask[i.cls], 16),
					regtoa(i.to.val, SLong)
				);
			emitf("add%k %0, %=", &i, fn, f);
			break;
		}
		goto Table;
	case Odiv:
		if (req(i.to, i.arg[1])) {
			i.arg[1] = TMP(XMM0+15);
			emitf("mov%k %=, %1", &i, fn, f);
			emitf("mov%k %0, %=", &i, fn, f);
			i.arg[0] = i.to;
		}
		goto Table;
	case Ocopy:
		if (req(i.to, R) || req(i.arg[0], R))
			break;
		if (isreg(i.to)
		&& rtype(i.arg[0]) == RCon
		&& i.cls == Kl
		&& fn->con[i.arg[0].val].type == CBits
		&& (val = fn->con[i.arg[0].val].bits.i) >= 0
		&& val <= UINT32_MAX) {
			emitf("movl %W0, %W=", &i, fn, f);
		} else if (isreg(i.to)
		&& rtype(i.arg[0]) == RCon
		&& fn->con[i.arg[0].val].type == CAddr) {
			emitf("lea%k %M0, %=", &i, fn, f);
		} else if (!req(i.arg[0], i.to))
			emitf("mov%k %0, %=", &i, fn, f);
		break;
	case Ocall:
		switch (rtype(i.arg[0])) {
		case RCon:
			fprintf(f, "\tcallq ");
			emitcon(&fn->con[i.arg[0].val], f);
			fprintf(f, "\n");
			break;
		case RTmp:
			emitf("callq *%L0", &i, fn, f);
			break;
		default:
			die("invalid call argument");
		}
		break;
	case Osalloc:
		emitf("subq %L0, %%rsp", &i, fn, f);
		if (!req(i.to, R))
			emitcopy(i.to, TMP(RSP), Kl, fn, f);
		break;
	case Oswap:
		if (KBASE(i.cls) == 0)
			goto Table;
		emitcopy(TMP(XMM0+15), i.arg[0], i.cls, fn, f);
		emitcopy(i.arg[0], i.arg[1], i.cls, fn, f);
		emitcopy(i.arg[1], TMP(XMM0+15), i.cls, fn, f);
		break;
	}
}

static uint64_t
framesz(Fn *fn, uint *save_gpr, uint *save_xmm, uint64_t *localsz)
{
	uint64_t f;
	int *r;

	*save_gpr = 0;
	*save_xmm = 0;
	for (r=amd64_win64_rclob; *r>=0; r++) {
		if (*r >= XMM0)
			*save_xmm += !!(fn->reg & BIT(*r));
		else
			*save_gpr += !!(fn->reg & BIT(*r));
	}

	f = fn->slot;
	f = (f + 3) & -4;
	f = 4*f;
	*localsz = f;
	f += 8 * (*save_gpr);
	f += 16 * (*save_xmm);
	f = (f + 15) & -16;
	return f;
}

void
amd64_win64_emitfn(Fn *fn, FILE *f)
{
	static char *ctoa[] = {
	#define X(c, s) [c] = s,
		CMP(X)
	#undef X
	};
	Blk *b, *s;
	Ins *i;
	int *r, c, lbl;
	uint save_gpr, save_xmm;
	uint64_t fs, off, localsz;
	uint64_t gpr_base, xmm_base;

	fprintf(f, ".text\n");
	if (fn->export)
		fprintf(f, ".globl %s%s\n", gassym, fn->name);
	fprintf(f,
		"%s%s:\n"
		"\tpushq %%rbp\n"
		"\tmovq %%rsp, %%rbp\n",
		gassym, fn->name
	);

	fs = framesz(fn, &save_gpr, &save_xmm, &localsz);
	if (fs)
		fprintf(f, "\tsub $%"PRIu64", %%rsp\n", fs);

	gpr_base = localsz;
	off = 0;
	for (r=amd64_win64_rclob; *r>=0 && *r < XMM0; r++) {
		if (!(fn->reg & BIT(*r)))
			continue;
		fprintf(f, "\tmovq %%%s, -%"PRIu64"(%%rbp)\n",
			regtoa(*r, SLong),
			gpr_base + off + 8);
		off += 8;
	}
	xmm_base = localsz + 8 * save_gpr;
	off = 0;
	for (; *r>=0; r++) {
		if (!(fn->reg & BIT(*r)))
			continue;
		fprintf(f, "\tmovdqu %%%s, -%"PRIu64"(%%rbp)\n",
			regtoa(*r, SLong),
			xmm_base + off + 16);
		off += 16;
	}

	for (lbl=0, b=fn->start; b; b=b->link) {
		if (lbl || b->npred > 1)
			fprintf(f, "%sbb%d:\n", gasloc, b->id);
		for (i=b->ins; i!=&b->ins[b->nins]; i++)
			emitins(*i, fn, f);
		lbl = 1;
		switch (b->jmp.type) {
		case Jret0:
			/* Restore callee-saved regs from the same slots they were saved to. */
			off = 0;
			for (r=&amd64_win64_rclob[0]; *r>=0; r++)
				if (*r >= XMM0 && (fn->reg & BIT(*r))) {
					fprintf(f, "\tmovdqu -%"PRIu64"(%%rbp), %%%s\n",
						xmm_base + off + 16,
						regtoa(*r, SLong));
					off += 16;
				}
			off = 0;
			for (r=&amd64_win64_rclob[0]; *r>=0 && *r < XMM0; r++)
				if (fn->reg & BIT(*r)) {
					fprintf(f, "\tmovq -%"PRIu64"(%%rbp), %%%s\n",
						gpr_base + off + 8,
						regtoa(*r, SLong));
					off += 8;
				}
			if (fn->dynalloc)
				fprintf(f,
					"\tmovq %%rbp, %%rsp\n"
					"\tsubq $%"PRIu64", %%rsp\n",
					fs
				);
			fprintf(f,
				"\tleave\n"
				"\tret\n"
			);
			break;
		case Jjmp:
		Jmp:
			if (b->s1 != b->link)
				fprintf(f, "\tjmp %sbb%d\n",
					gasloc, b->s1->id);
			else
				lbl = 0;
			break;
		default:
			c = b->jmp.type - Jjf;
			if (0 <= c && c <= NCmp) {
				if (b->link == b->s2) {
					s = b->s1;
					b->s1 = b->s2;
					b->s2 = s;
				} else
					c = cmpneg(c);
				fprintf(f, "\tj%s %sbb%d\n", ctoa[c],
					gasloc, b->s2->id);
				goto Jmp;
			}
			die("unhandled jump %d", b->jmp.type);
		}
	}
}
