.data
.align 8
str1:
	.ascii "index out of bounds"
	.byte 0
/* end data */

.data
.align 8
str2:
	.ascii "Done"
	.byte 0
/* end data */

.text
.globl test_multiple_panics
test_multiple_panics:
	pushq %rbp
	movq %rsp, %rbp
	sub $8, %rsp
	pushq %rbx
	movq %rdi, %rbx
	callq ferret_array_len
	movq %rbx, %rdi
	cmpl $0, %eax
	setle %al
	movzbl %al, %eax
	orl $0, %eax
	jnz .Lbb16
	movl $0, %esi
	movq %rdi, %rbx
	callq ferret_array_get
	movq %rbx, %rdi
	movq %rdi, %rbx
	callq ferret_array_len
	movq %rbx, %rdi
	cmpl $1, %eax
	setle %al
	movzbl %al, %eax
	orl $0, %eax
	jnz .Lbb15
	movl $1, %esi
	movq %rdi, %rbx
	callq ferret_array_get
	movq %rbx, %rdi
	movq %rdi, %rbx
	callq ferret_array_len
	movq %rbx, %rdi
	cmpl $2, %eax
	setle %al
	movzbl %al, %eax
	orl $0, %eax
	jnz .Lbb14
	subq $16, %rsp
	movq %rsp, %rdx
	movl $42, (%rdx)
	movl $2, %esi
	movq %rdi, %rbx
	callq ferret_array_set
	movq %rbx, %rdi
	cmpl $0, %eax
	jz .Lbb13
	movq %rdi, %rbx
	callq ferret_array_len
	movq %rbx, %rdi
	cmpl $3, %eax
	setle %al
	movzbl %al, %eax
	orl $0, %eax
	jnz .Lbb12
	subq $16, %rsp
	movq %rsp, %rdx
	movl $43, (%rdx)
	movl $3, %esi
	callq ferret_array_set
	cmpl $0, %eax
	jz .Lbb11
	movq %rbp, %rsp
	subq $16, %rsp
	popq %rbx
	leave
	ret
.Lbb11:
	leaq str1(%rip), %rdi
	callq ferret_global_panic
	movq %rbp, %rsp
	subq $16, %rsp
	popq %rbx
	leave
	ret
.Lbb12:
	leaq str1(%rip), %rdi
	callq ferret_global_panic
	movq %rbp, %rsp
	subq $16, %rsp
	popq %rbx
	leave
	ret
.Lbb13:
	leaq str1(%rip), %rdi
	callq ferret_global_panic
	movq %rbp, %rsp
	subq $16, %rsp
	popq %rbx
	leave
	ret
.Lbb14:
	leaq str1(%rip), %rdi
	callq ferret_global_panic
	movq %rbp, %rsp
	subq $16, %rsp
	popq %rbx
	leave
	ret
.Lbb15:
	leaq str1(%rip), %rdi
	callq ferret_global_panic
	movq %rbp, %rsp
	subq $16, %rsp
	popq %rbx
	leave
	ret
.Lbb16:
	leaq str1(%rip), %rdi
	callq ferret_global_panic
	movq %rbp, %rsp
	subq $16, %rsp
	popq %rbx
	leave
	ret
/* end function test_multiple_panics */

.text
.globl main
main:
	pushq %rbp
	movq %rsp, %rbp
	sub $72, %rsp
	pushq %rbx
	movl $4, %esi
	movl $4, %edi
	callq ferret_array_new
	movq %rax, %rdi
	movl $1, -56(%rbp)
	leaq -56(%rbp), %rsi
	movq %rdi, %rbx
	callq ferret_array_append
	movq %rbx, %rdi
	movl $2, -52(%rbp)
	leaq -52(%rbp), %rsi
	movq %rdi, %rbx
	callq ferret_array_append
	movq %rbx, %rdi
	movl $3, -48(%rbp)
	leaq -48(%rbp), %rsi
	movq %rdi, %rbx
	callq ferret_array_append
	movq %rbx, %rdi
	movl $4, -44(%rbp)
	leaq -44(%rbp), %rsi
	movq %rdi, %rbx
	callq ferret_array_append
	movq %rbx, %rdi
	callq ferret_array_clone
	movq %rax, %rdi
	callq test_multiple_panics
	movl $1, %esi
	movl $36, %edi
	callq ferret_array_new
	movq %rax, %rdi
	movl $16, -40(%rbp)
	movq $str2, -36(%rbp)
	leaq -40(%rbp), %rsi
	movq %rdi, %rbx
	callq ferret_array_append
	movq %rbx, %rdi
	callq ferret_std_io_Println
	movl $0, %eax
	popq %rbx
	leave
	ret
/* end function main */

