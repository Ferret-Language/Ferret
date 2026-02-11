#include "../all.h"

typedef struct Amd64Op Amd64Op;

enum Amd64Reg {
	RAX = RXX+1, /* caller-save */
	RCX,  /* caller-save */
	RDX,  /* caller-save */
	RSI,  /* caller-save on sysv, callee-save on win */
	RDI,  /* caller-save on sysv, callee-save on win */
	R8,  /* caller-save */
	R9,  /* caller-save */
	R10,  /* caller-save */
	R11,  /* caller-save */

	RBX, /* callee-save */
	R12,
	R13,
	R14,
	R15,

	RBP, /* globally live */
	RSP,

	XMM0, /* sse */
	XMM1,
	XMM2,
	XMM3,
	XMM4,
	XMM5,
	XMM6,
	XMM7,
	XMM8,
	XMM9,
	XMM10,
	XMM11,
	XMM12,
	XMM13,
	XMM14,
	XMM15,

	NFPR = XMM14 - XMM0 + 1, /* reserve XMM15 */
	NGPR = RSP - RAX + 1,
	NFPS = NFPR,

	NGPS_SYSV = R11 - RAX + 1,
	NCLR_SYSV = R15 - RBX + 1,

	NGPS_WIN = R11 - RAX + 1 - 2,  /* -2 for RDI/RDI */
	NCLR_WIN = R15 - RBX + 1 + 2,  /* +2 for RDI/RDI */
};
MAKESURE(reg_not_tmp, XMM15 < (int)Tmp0);

struct Amd64Op {
	char nmem;
	char zflag;
	char lflag;
};

/* targ.c */
extern Amd64Op amd64_op[];

/* sysv.c (abi) */
extern int amd64_sysv_rsave[];
extern int amd64_sysv_rclob[];
bits amd64_sysv_retregs(Ref, int[2]);
bits amd64_sysv_argregs(Ref, int[2]);
void amd64_sysv_abi(Fn *);

/* winabi.c */
extern int amd64_winabi_rsave[];
extern int amd64_winabi_rclob[];
bits amd64_winabi_retregs(Ref, int[2]);
bits amd64_winabi_argregs(Ref, int[2]);
void amd64_winabi_abi(Fn *);

/* isel.c */
void amd64_isel(Fn *);

/* emit.c */
void amd64_sysv_emitfn(Fn *, FILE *);
void amd64_winabi_emitfn(Fn *, FILE *);
