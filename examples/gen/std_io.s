.data
.align 8
str1:
	.ascii "index out of bounds"
	.byte 0
/* end data */

.text
.globl std_io_Printf
std_io_Printf:
	pushq %rbp
	movq %rsp, %rbp
	sub $72, %rsp
	pushq %rbx
	pushq %r12
	pushq %r13
	pushq %r14
	pushq %r15
	movq %rsi, %r14
	movq %rdi, %rbx
	movl $0, %edi
	callq ferret_array_clone
	movq %rbx, %rdi
	movq %rax, -64(%rbp)
	callq ferret_string_to_char_array
	movq %rax, %rsi
	leaq -64(%rbp), %rdi
	callq ferret_array_assign
	movl $0, %r12d
	movl $0, %ebx
.Lbb2:
	movq -64(%rbp), %rdi
	callq ferret_array_len
	cmpl %eax, %r12d
	jge .Lbb35
	movl %r12d, %r13d
	addl $1, %r13d
	movq -64(%rbp), %rdi
	callq ferret_array_len
	cmpl %eax, %r13d
	jl .Lbb5
	movq %r14, %r13
	movl $0, %eax
	jmp .Lbb11
.Lbb5:
	movq -64(%rbp), %rdi
	movq %rdi, %r13
	callq ferret_array_len
	movq %r14, %rsi
	movq %r13, %rdi
	movl %eax, %ecx
	cmpl $0, %r12d
	jl .Lbb7
	movl %r12d, %r14d
	jmp .Lbb9
.Lbb7:
	movl %ecx, %r13d
	addl %r12d, %r13d
	movl %r13d, %r14d
.Lbb9:
	cmpl $0, %r14d
	setl %al
	movzbl %al, %eax
	cmpl %ecx, %r14d
	setge %cl
	movzbl %cl, %ecx
	orl %ecx, %eax
	jnz .Lbb34
	movq %rsi, %r13
	movl %r14d, %esi
	callq ferret_array_get
	movl (%rax), %eax
	cmpl $123, %eax
	setz %al
	movzbl %al, %eax
.Lbb11:
	movzbl %al, %eax
	cmpl $0, %eax
	jnz .Lbb13
	movq %r13, %rsi
	movl $0, %eax
	jmp .Lbb17
.Lbb13:
	movq -64(%rbp), %rdi
	movl %r12d, %r15d
	addl $1, %r15d
	movq %rdi, %r14
	callq ferret_array_len
	movl %r15d, %esi
	movq %r14, %rdi
	movl %eax, %ecx
	cmpl $0, %esi
	jge .Lbb15
	addl %ecx, %esi
.Lbb15:
	cmpl $0, %esi
	setl %al
	movzbl %al, %eax
	cmpl %ecx, %esi
	setge %cl
	movzbl %cl, %ecx
	orl %ecx, %eax
	jnz .Lbb33
	callq ferret_array_get
	movq %r13, %rsi
	movl (%rax), %eax
	cmpl $125, %eax
	setz %al
	movzbl %al, %eax
.Lbb17:
	movzbl %al, %eax
	cmpl $0, %eax
	jz .Lbb24
	movq %rsi, %r14
	movl $1, %esi
	movl $36, %edi
	callq ferret_array_new
	movq %rax, %rdi
	movq %rdi, %r13
	movq %r14, %rdi
	callq ferret_array_len
	movq %r14, %rsi
	movq %r13, %rdi
	movl %eax, %ecx
	cmpl $0, %ebx
	jl .Lbb20
	movl %ebx, %r14d
	jmp .Lbb22
.Lbb20:
	movl %ecx, %r13d
	addl %ebx, %r13d
	movl %r13d, %r14d
.Lbb22:
	cmpl $0, %r14d
	setl %al
	movzbl %al, %eax
	cmpl %ecx, %r14d
	setge %cl
	movzbl %cl, %ecx
	orl %ecx, %eax
	jnz .Lbb32
	movq %rsi, %r13
	movl %r14d, %esi
	movq %rdi, %r14
	movq %r13, %rdi
	callq ferret_array_get
	movq %r14, %rdi
	movq %rax, %rsi
	movq %rdi, %r14
	callq ferret_array_append
	movq %r14, %rdi
	callq ferret_std_io_Print
	movq %r13, %rsi
	addl $2, %r12d
	addl $1, %ebx
.Lbb24:
	movq %rsi, %r15
	movl $1, %esi
	movl $36, %edi
	callq ferret_array_new
	movq %rax, %r13
	movq -64(%rbp), %rdi
	movq %rdi, %r14
	callq ferret_array_len
	movq %r15, %rsi
	movq %r14, %rdi
	movl %eax, %ecx
	cmpl $0, %r12d
	jl .Lbb26
	movl %r12d, %r15d
	jmp .Lbb28
.Lbb26:
	movl %ecx, %r14d
	addl %r12d, %r14d
	movl %r14d, %r15d
.Lbb28:
	cmpl $0, %r15d
	setl %al
	movzbl %al, %eax
	cmpl %ecx, %r15d
	setge %cl
	movzbl %cl, %ecx
	orl %ecx, %eax
	jnz .Lbb31
	movq %rsi, %r14
	movl %r15d, %esi
	callq ferret_array_get
	movq %r14, %rsi
	movq %r13, %rdi
	movl (%rax), %eax
	movl $18, -40(%rbp)
	movl %eax, -36(%rbp)
	movq %rsi, %r13
	leaq -40(%rbp), %rsi
	movq %rdi, %r14
	callq ferret_array_append
	movq %r14, %rdi
	callq ferret_std_io_Print
	movq %r13, %rsi
	addl $1, %r12d
	movq %rsi, %r14
	jmp .Lbb2
.Lbb31:
	leaq str1(%rip), %rdi
	callq ferret_global_panic
	popq %r15
	popq %r14
	popq %r13
	popq %r12
	popq %rbx
	leave
	ret
.Lbb32:
	leaq str1(%rip), %rdi
	callq ferret_global_panic
	popq %r15
	popq %r14
	popq %r13
	popq %r12
	popq %rbx
	leave
	ret
.Lbb33:
	leaq str1(%rip), %rdi
	callq ferret_global_panic
	popq %r15
	popq %r14
	popq %r13
	popq %r12
	popq %rbx
	leave
	ret
.Lbb34:
	leaq str1(%rip), %rdi
	callq ferret_global_panic
	popq %r15
	popq %r14
	popq %r13
	popq %r12
	popq %rbx
	leave
	ret
.Lbb35:
	popq %r15
	popq %r14
	popq %r13
	popq %r12
	popq %rbx
	leave
	ret
/* end function std_io_Printf */

