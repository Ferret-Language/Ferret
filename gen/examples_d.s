.data
.align 8
str1:
	.ascii "x"
	.byte 0
/* end data */

.data
.align 8
str2:
	.ascii "g"
	.byte 0
/* end data */

.data
.align 8
str3:
	.ascii "start"
	.byte 0
/* end data */

.data
.align 8
str4:
	.ascii "final"
	.byte 0
/* end data */

.data
.align 8
str5:
	.ascii "len"
	.byte 0
/* end data */

.data
.align 8
str6:
	.ascii "guard len"
	.byte 0
/* end data */

.data
.align 8
str7:
	.ascii "moved"
	.byte 0
/* end data */

.data
.align 8
str8:
	.ascii "->"
	.byte 0
/* end data */

.text
.globl main
main:
	pushq %rbp
	movq %rsp, %rbp
	sub $632, %rsp
	pushq %rbx
	pushq %r12
	pushq %r13
	movq $0, -616(%rbp)
	leaq str1(%rip), %rsi
	leaq -616(%rbp), %rdi
	callq ferret_string_assign
	movq $0, -608(%rbp)
	leaq str2(%rip), %rsi
	leaq -608(%rbp), %rdi
	callq ferret_string_assign
	movq -616(%rbp), %rbx
	movl $3, %esi
	movl $36, %edi
	callq ferret_array_new
	movq %rax, %rdi
	movl $16, -600(%rbp)
	movq $str3, -596(%rbp)
	leaq -600(%rbp), %rsi
	movq %rdi, %r12
	callq ferret_array_append
	movq %r12, %rdi
	movl $9, -560(%rbp)
	movq %rbx, -556(%rbp)
	leaq -560(%rbp), %rsi
	movq %rdi, %r12
	callq ferret_array_append
	movq %r12, %rdi
	movq %rdi, %r12
	movq -616(%rbp), %rdi
	callq ferret_string_len
	movq %r12, %rdi
	movl $2, -520(%rbp)
	movl %eax, -516(%rbp)
	leaq -520(%rbp), %rsi
	movq %rdi, %r12
	callq ferret_array_append
	movq %r12, %rdi
	callq ferret_std_io_Println
	movl $0, %r12d
.Lbb2:
	cmpl $250, %r12d
	setl %r13b
	movzbl %r13b, %r13d
	movq -616(%rbp), %rdi
	callq ferret_string_len
	cmpl $10000, %eax
	setl %al
	movzbl %al, %eax
	testl %r13d, %eax
	jz .Lbb6
	movq -608(%rbp), %rdi
	movq %rdi, %rsi
	callq ferret_io_ConcatStrings
	movq %rax, %rsi
	leaq -608(%rbp), %rdi
	callq ferret_string_assign
	movq -616(%rbp), %rdi
	movq %rdi, %rsi
	callq ferret_io_ConcatStrings
	movq %rax, %rsi
	leaq -616(%rbp), %rdi
	callq ferret_string_assign
	cmpq %rbx, %rbx
	jnz .Lbb5
	addl $1, %r12d
	jmp .Lbb2
.Lbb5:
	movl $6, %esi
	movl $36, %edi
	callq ferret_array_new
	movq %rax, %rdi
	movl $16, -240(%rbp)
	movq $str7, -236(%rbp)
	leaq -240(%rbp), %rsi
	movq %rdi, %r12
	callq ferret_array_append
	movq %r12, %rdi
	movl $9, -200(%rbp)
	movq %rbx, -196(%rbp)
	leaq -200(%rbp), %rsi
	movq %rdi, %r12
	callq ferret_array_append
	movq %r12, %rdi
	movl $16, -160(%rbp)
	movq $str8, -156(%rbp)
	leaq -160(%rbp), %rsi
	movq %rdi, %r12
	callq ferret_array_append
	movq %r12, %rdi
	movl $9, -120(%rbp)
	movq %rbx, -116(%rbp)
	leaq -120(%rbp), %rsi
	movq %rdi, %r12
	callq ferret_array_append
	movq %r12, %rdi
	movl $16, -80(%rbp)
	movq $str5, -76(%rbp)
	leaq -80(%rbp), %rsi
	movq %rdi, %r12
	callq ferret_array_append
	movq %r12, %rdi
	movq %rdi, %r12
	movq -616(%rbp), %rdi
	callq ferret_string_len
	movq %r12, %rdi
	movl $2, -40(%rbp)
	movl %eax, -36(%rbp)
	leaq -40(%rbp), %rsi
	movq %rdi, %r12
	callq ferret_array_append
	movq %r12, %rdi
	callq ferret_std_io_Println
.Lbb6:
	movl $4, %esi
	movl $36, %edi
	callq ferret_array_new
	movq %rax, %rdi
	movl $16, -480(%rbp)
	movq $str4, -476(%rbp)
	leaq -480(%rbp), %rsi
	movq %rdi, %r12
	callq ferret_array_append
	movq %r12, %rdi
	movl $9, -440(%rbp)
	movq %rbx, -436(%rbp)
	leaq -440(%rbp), %rsi
	movq %rdi, %rbx
	callq ferret_array_append
	movq %rbx, %rdi
	movl $16, -400(%rbp)
	movq $str5, -396(%rbp)
	leaq -400(%rbp), %rsi
	movq %rdi, %rbx
	callq ferret_array_append
	movq %rbx, %rdi
	movq %rdi, %rbx
	movq -616(%rbp), %rdi
	callq ferret_string_len
	movq %rbx, %rdi
	movl $2, -360(%rbp)
	movl %eax, -356(%rbp)
	leaq -360(%rbp), %rsi
	movq %rdi, %rbx
	callq ferret_array_append
	movq %rbx, %rdi
	callq ferret_std_io_Println
	movl $2, %esi
	movl $36, %edi
	callq ferret_array_new
	movq %rax, %rdi
	movl $16, -320(%rbp)
	movq $str6, -316(%rbp)
	leaq -320(%rbp), %rsi
	movq %rdi, %rbx
	callq ferret_array_append
	movq %rbx, %rdi
	movq %rdi, %rbx
	movq -608(%rbp), %rdi
	callq ferret_string_len
	movq %rbx, %rdi
	movl $2, -280(%rbp)
	movl %eax, -276(%rbp)
	leaq -280(%rbp), %rsi
	movq %rdi, %rbx
	callq ferret_array_append
	movq %rbx, %rdi
	callq ferret_std_io_Println
	movl $0, %eax
	popq %r13
	popq %r12
	popq %rbx
	leave
	ret
/* end function main */

