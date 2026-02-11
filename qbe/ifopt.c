#include "all.h"

enum {
	MaxIns = 2,
	MaxPhis = 2,
};

static int
okbranch(Blk *b)
{
	Ins *i;
	int n;

	n = 0;
	for (i=b->ins; i<&b->ins[b->nins]; i++)
		if (i->op != Odbgloc) {
			if (pinned(i))
				return 0;
			if (i->op != Onop)
				n++;
		}
	return n <= MaxIns;
}

static int
okjoin(Blk *b)
{
	Phi *p;
	int n;

	n = 0;
	for (p=b->phi; p; p=p->link) {
		if (KBASE(p->cls) != 0)
			return 0;
		n++;
	}
	return n <= MaxPhis;
}

static int
okgraph(Blk *ifb, Blk *thenb, Blk *elseb, Blk *joinb)
{
	if (joinb->npred != 2 || !okjoin(joinb))
		return 0;
	assert(thenb != elseb);
	if (thenb != ifb && !okbranch(thenb))
		return 0;
	if (elseb != ifb && !okbranch(elseb))
		return 0;
	return 1;
}

static void
convert(Blk *ifb, Blk *thenb, Blk *elseb, Blk *joinb)
{
	Ins *ins, sel;
	Phi *p;
	uint nins;

	ins = vnew(0, sizeof ins[0], PHeap);
	nins = 0;
	addbins(&ins, &nins, ifb);
	if (thenb != ifb)
		addbins(&ins, &nins, thenb);
	if (elseb != ifb)
		addbins(&ins, &nins, elseb);
	assert(joinb->npred == 2);
	if (joinb->phi) {
		sel = (Ins){
			.op = Osel0, .cls = Kw,
			.arg = {ifb->jmp.arg},
		};
		addins(&ins, &nins, &sel);
	}
	sel = (Ins){.op = Osel1};
	for (p=joinb->phi; p; p=p->link) {
		sel.to = p->to;
		sel.cls = p->cls;
		sel.arg[0] = phiarg(p, thenb);
		sel.arg[1] = phiarg(p, elseb);
		addins(&ins, &nins, &sel);
	}
	idup(ifb, ins, nins);
	ifb->jmp.type = Jjmp;
	ifb->jmp.arg = R;
	ifb->s1 = joinb;
	ifb->s2 = 0;
	joinb->npred = 1;
	joinb->pred[0] = ifb;
	joinb->phi = 0;
	vfree(ins);
}

/* eliminate if-then[-else] graphlets
 * using sel instructions
 * needs rpo pred use; breaks cfg use
 */
void
ifconvert(Fn *fn)
{
	Blk *ifb, *thenb, *elseb, *joinb;

	if (debug['K'])
		fputs("\n> If-conversion:\n", stderr);

	for (ifb=fn->start; ifb; ifb=ifb->link)
		if (ifgraph(ifb, &thenb, &elseb, &joinb))
		if (okgraph(ifb, thenb, elseb, joinb)) {
			if (debug['K'])
				fprintf(stderr,
					"    @%s -> @%s, @%s -> @%s\n",
					ifb->name, thenb->name, elseb->name,
					joinb->name);
			convert(ifb, thenb, elseb, joinb);
		}

	if (debug['K']) {
		fprintf(stderr, "\n> After if-conversion:\n");
		printfn(fn, stderr);
	}
}
