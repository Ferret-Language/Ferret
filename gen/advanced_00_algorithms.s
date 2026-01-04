.data
.align 8
str1:
	.ascii "index out of bounds"
	.byte 0
/* end data */

.text
.globl factorial
factorial:
	pushq %rbp
	movq %rsp, %rbp
	cmpl $1, %edi
	jle .Lbb5
	movl $1, %eax
	movl $2, %ecx
.Lbb2:
	cmpl %edi, %ecx
	jg .Lbb4
	imull %ecx, %eax
	addl $1, %ecx
	jmp .Lbb2
.Lbb4:
	leave
	ret
.Lbb5:
	movl $1, %eax
	leave
	ret
/* end function factorial */

.text
.globl fib
fib:
	pushq %rbp
	movq %rsp, %rbp
	movl %edi, %eax
	cmpl $1, %eax
	jle .Lbb16
	movl %eax, %edi
	movl $0, %esi
	movl $1, %ecx
	movl $1, %edx
	movl $2, %eax
.Lbb8:
	movzbl %dl, %r8d
	cmpl $0, %r8d
	jnz .Lbb10
	addl $1, %eax
	jmp .Lbb11
.Lbb10:
	movl $0, %edx
.Lbb11:
	cmpl %edi, %eax
	setle %r8b
	movzbl %r8b, %r8d
	cmpl $0, %r8d
	jz .Lbb14
	addl %ecx, %esi
	xchgl %ecx, %esi
	jmp .Lbb8
.Lbb14:
	movl %ecx, %eax
	leave
	ret
.Lbb16:
	leave
	ret
/* end function fib */

.text
.globl gcd
gcd:
	pushq %rbp
	movq %rsp, %rbp
	movl %esi, %ecx
	movl %edi, %eax
	movl %ecx, %esi
	movl %eax, %ecx
.Lbb19:
	cmpl $0, %esi
	jz .Lbb22
	movl %ecx, %eax
	cltd
	idivl %esi
	movl %edx, %ecx
	xchgl %esi, %ecx
	jmp .Lbb19
.Lbb22:
	movl %ecx, %eax
	leave
	ret
/* end function gcd */

.text
.globl binary_search
binary_search:
	pushq %rbp
	movq %rsp, %rbp
	sub $8, %rsp
	pushq %rbx
	pushq %r12
	pushq %r13
	pushq %r14
	pushq %r15
	movl %esi, %r15d
	movq %rdi, %rbx
	callq ferret_array_len
	movq %rbx, %rdi
	movl %eax, %ebx
	subl $1, %ebx
	movl $0, %r12d
.Lbb26:
	movl %ebx, %r13d
	cmpl %r13d, %r12d
	jg .Lbb40
	movl %r12d, %eax
	addl %r13d, %eax
	movl $2, %ecx
	cltd
	idivl %ecx
	movl %eax, %ebx
	movq %rdi, %r14
	callq ferret_array_len
	movl %r15d, %esi
	movq %r14, %rdi
	movl %eax, %ecx
	cmpl $0, %ebx
	jl .Lbb29
	movl %ebx, %r14d
	jmp .Lbb30
.Lbb29:
	movl %ecx, %r14d
	addl %ebx, %r14d
.Lbb30:
	cmpl $0, %r14d
	setl %al
	movzbl %al, %eax
	cmpl %ecx, %r14d
	setge %cl
	movzbl %cl, %ecx
	orl %ecx, %eax
	jnz .Lbb39
	movl %esi, %r15d
	movl %r14d, %esi
	movq %rdi, %r14
	callq ferret_array_get
	movl %r15d, %esi
	movq %r14, %rdi
	movl (%rax), %eax
	cmpl %esi, %eax
	jz .Lbb37
	cmpl %esi, %eax
	jl .Lbb34
	subl $1, %ebx
	jmp .Lbb36
.Lbb34:
	movl %ebx, %r12d
	movl %r13d, %ebx
	addl $1, %r12d
.Lbb36:
	movl %esi, %r15d
	jmp .Lbb26
.Lbb37:
	movl %ebx, %eax
	popq %r15
	popq %r14
	popq %r13
	popq %r12
	popq %rbx
	leave
	ret
.Lbb39:
	leaq str1(%rip), %rdi
	callq ferret_global_panic
	popq %r15
	popq %r14
	popq %r13
	popq %r12
	popq %rbx
	leave
	ret
.Lbb40:
	movl $-1, %eax
	popq %r15
	popq %r14
	popq %r13
	popq %r12
	popq %rbx
	leave
	ret
/* end function binary_search */

