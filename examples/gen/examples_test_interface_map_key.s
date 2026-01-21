.data
.align 8
__typeid_2:
	.byte 67
	.byte 97
	.byte 116
	.byte 0
/* end data */

.data
.align 8
__typeid_1:
	.byte 68
	.byte 111
	.byte 103
	.byte 0
/* end data */

.data
.align 8
typedesc1:
	.int 13
	.int 0
	.quad 8
	.quad 0
	.quad 0
/* end data */

.data
.align 8
str1:
	.ascii "Rex"
	.byte 0
/* end data */

.data
.align 8
str2:
	.ascii "Whiskers"
	.byte 0
/* end data */

.data
.align 8
str3:
	.ascii "Barks"
	.byte 0
/* end data */

.data
.align 8
str4:
	.ascii "Meows"
	.byte 0
/* end data */

.data
.align 8
str5:
	.ascii "Interface map keys added successfully!"
	.byte 0
/* end data */

.text
.globl main
main:
	pushq %rbp
	movq %rsp, %rbp
	sub $152, %rsp
	pushq %rbx
	movl $0, %edi
	callq ferret_map_clone
	movq %rax, -144(%rbp)
	leaq typedesc1(%rip), %rdx
	movl $8, %esi
	movl $16, %edi
	callq ferret_map_new_universal
	movq %rax, %rsi
	leaq -144(%rbp), %rdi
	callq ferret_map_assign
	movq $str1, -136(%rbp)
	movq $str2, -128(%rbp)
	movl $8, %edi
	callq ferret_alloc
	movq %rax, %rbx
	movl $8, %edx
	leaq -136(%rbp), %rsi
	movq %rbx, %rdi
	callq ferret_memcpy
	movq %rbx, -104(%rbp)
	movq $__typeid_1, -96(%rbp)
	movl $16, %edx
	leaq -104(%rbp), %rsi
	leaq -120(%rbp), %rdi
	callq ferret_memcpy
	movl $8, %edi
	callq ferret_alloc
	movq %rax, %rbx
	movl $8, %edx
	leaq -128(%rbp), %rsi
	movq %rbx, %rdi
	callq ferret_memcpy
	movq %rbx, -72(%rbp)
	movq $__typeid_2, -64(%rbp)
	movl $16, %edx
	leaq -72(%rbp), %rsi
	leaq -88(%rbp), %rdi
	callq ferret_memcpy
	movq -144(%rbp), %rdi
	movq $str3, -16(%rbp)
	leaq -16(%rbp), %rdx
	leaq -120(%rbp), %rsi
	callq ferret_map_set
	movq -144(%rbp), %rdi
	movq $str4, -8(%rbp)
	leaq -8(%rbp), %rdx
	leaq -88(%rbp), %rsi
	callq ferret_map_set
	movl $1, %esi
	movl $36, %edi
	callq ferret_array_new
	movq %rax, %rdi
	movl $16, -56(%rbp)
	movq $str5, -52(%rbp)
	leaq -56(%rbp), %rsi
	movq %rdi, %rbx
	callq ferret_array_append
	movq %rbx, %rdi
	callq ferret_std_io_Println
	movl $0, %eax
	popq %rbx
	leave
	ret
/* end function main */

