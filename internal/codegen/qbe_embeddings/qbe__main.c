#include "qbe_embed.h"

#include <getopt.h>
#include <setjmp.h>

static jmp_buf qbe_jmp_buf;
static int qbe_exit_code;

#define exit qbe_exit
#define main qbe_main
#include "../../../qbe/main.c"
#undef main
#undef exit

void qbe_exit(int code) {
	qbe_exit_code = code;
	if (outf) {
		fflush(outf);
		if (outf != stdout && outf != stderr) {
			fclose(outf);
		}
	}
	longjmp(qbe_jmp_buf, 1);
}

int ferret_qbe_run(int argc, char **argv) {
	qbe_exit_code = 0;
	if (setjmp(qbe_jmp_buf) != 0) {
		return qbe_exit_code;
	}
	optind = 1;
	qbe_main(argc, argv);
	return qbe_exit_code;
}
