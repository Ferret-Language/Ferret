.data
.align 8
__typeid_2:
	.byte 38
	.byte 105
	.byte 51
	.byte 50
	.byte 0
/* end data */

.data
.align 8
__typeid_1:
	.byte 38
	.byte 115
	.byte 116
	.byte 114
	.byte 117
	.byte 99
	.byte 116
	.byte 32
	.byte 123
	.byte 32
	.byte 46
	.byte 88
	.byte 58
	.byte 32
	.byte 105
	.byte 51
	.byte 50
	.byte 44
	.byte 32
	.byte 46
	.byte 89
	.byte 58
	.byte 32
	.byte 105
	.byte 51
	.byte 50
	.byte 32
	.byte 125
	.byte 0
/* end data */

.data
.align 8
str1:
	.ascii " stack ="
	.byte 0
/* end data */

.data
.align 8
str2:
	.ascii " heap ="
	.byte 0
/* end data */

.data
.align 8
str3:
	.ascii "make: a"
	.byte 0
/* end data */

.data
.align 8
str4:
	.ascii "a"
	.byte 0
/* end data */

.data
.align 8
str5:
	.ascii "b"
	.byte 0
/* end data */

.data
.align 8
str6:
	.ascii "c"
	.byte 0
/* end data */

.data
.align 8
str7:
	.ascii "------------"
	.byte 0
/* end data */

.data
.align 8
str8:
	.ascii "x"
	.byte 0
/* end data */

.data
.align 8
str9:
	.ascii "small"
	.byte 0
/* end data */

.data
.align 8
str10:
	.ascii "small2"
	.byte 0
/* end data */

.data
.align 8
str11:
	.ascii "hmm"
	.byte 0
/* end data */

.data
.align 8
str12:
	.ascii "a very long text which should overflow the initial capacity"
	.byte 0
/* end data */

.text
.globl print_addrs
print_addrs:
	pushq %rbp
	movq %rsp, %rbp
	sub $208, %rsp
	pushq %rbx
	pushq %r12
	movq %rdi, %rbx
	movq %rsi, %r12
	movl $5, %esi
	movl $36, %edi
	callq ferret_array_new
	movq %r12, %rsi
	movq %rax, %rdi
	movl $16, -200(%rbp)
	movq %rbx, -196(%rbp)
	movq %rsi, %r12
	leaq -200(%rbp), %rsi
	movq %rdi, %rbx
	callq ferret_array_append
	movq %r12, %rsi
	movq %rbx, %rdi
	movl $16, -160(%rbp)
	movq $str1, -156(%rbp)
	movq %rsi, %r12
	leaq -160(%rbp), %rsi
	movq %rdi, %rbx
	callq ferret_array_append
	movq %r12, %rsi
	movq %rbx, %rdi
	movl $9, -120(%rbp)
	movq %rsi, -116(%rbp)
	movq %rsi, %r12
	leaq -120(%rbp), %rsi
	movq %rdi, %rbx
	callq ferret_array_append
	movq %r12, %rsi
	movq %rbx, %rdi
	movl $16, -80(%rbp)
	movq $str2, -76(%rbp)
	movq %rsi, %r12
	leaq -80(%rbp), %rsi
	movq %rdi, %rbx
	callq ferret_array_append
	movq %r12, %rsi
	movq %rbx, %rdi
	movq 0(%rsi), %rax
	movl $9, -40(%rbp)
	movq %rax, -36(%rbp)
	leaq -40(%rbp), %rsi
	movq %rdi, %rbx
	callq ferret_array_append
	movq %rbx, %rdi
	callq ferret_std_io_Println
	popq %r12
	popq %rbx
	leave
	ret
/* end function print_addrs */

.text
.globl make
make:
	pushq %rbp
	movq %rsp, %rbp
	sub $40, %rsp
	pushq %rbx
	movl $10, -24(%rbp)
	movl $20, -20(%rbp)
	movl $8, %edi
	callq ferret_alloc
	movq %rax, %rbx
	movl $8, %edx
	leaq -24(%rbp), %rsi
	movq %rbx, %rdi
	callq ferret_memcpy
	movq %rbx, -16(%rbp)
	movq $__typeid_1, -8(%rbp)
	leaq -16(%rbp), %rdx
	leaq -16(%rbp), %rsi
	leaq str3(%rip), %rdi
	callq print_addrs
	movq %rbx, %rax
	popq %rbx
	leave
	ret
/* end function make */

.text
.globl main
main:
	pushq %rbp
	movq %rsp, %rbp
	sub $2000, %rsp
	pushq %rbx
	pushq %r12
	pushq %r13
	pushq %r14
	movl $4, %edi
	callq ferret_alloc
	movq %rax, %r13
	movl $10, (%r13)
	movq %r13, -1992(%rbp)
	movl $4, %edi
	callq ferret_alloc
	movq %rax, %r12
	movl $20, (%r12)
	movq %r12, -1984(%rbp)
	movl $4, %edi
	callq ferret_alloc
	movq %rax, %rbx
	movl $30, (%rbx)
	movq %rbx, -1976(%rbp)
	movq %r13, -1968(%rbp)
	movq $__typeid_2, -1960(%rbp)
	leaq -1968(%rbp), %rdx
	leaq -1968(%rbp), %rsi
	leaq str4(%rip), %rdi
	callq print_addrs
	movq %r12, -1952(%rbp)
	movq $__typeid_2, -1944(%rbp)
	leaq -1952(%rbp), %rdx
	leaq -1952(%rbp), %rsi
	leaq str5(%rip), %rdi
	callq print_addrs
	movq %rbx, -1936(%rbp)
	movq $__typeid_2, -1928(%rbp)
	leaq -1936(%rbp), %rdx
	leaq -1936(%rbp), %rsi
	leaq str6(%rip), %rdi
	callq print_addrs
	movl $1, %esi
	movl $36, %edi
	callq ferret_array_new
	movq %rax, %rdi
	movl $16, -1920(%rbp)
	movq $str7, -1916(%rbp)
	leaq -1920(%rbp), %rsi
	movq %rdi, %r14
	callq ferret_array_append
	movq %r14, %rdi
	callq ferret_std_io_Println
	movl $5, %esi
	movl $36, %edi
	callq ferret_array_new
	movq %rax, %rdi
	movl $16, -1880(%rbp)
	movq $str4, -1876(%rbp)
	leaq -1880(%rbp), %rsi
	movq %rdi, %r14
	callq ferret_array_append
	movq %r14, %rdi
	movl $16, -1840(%rbp)
	movq $str1, -1836(%rbp)
	leaq -1840(%rbp), %rsi
	movq %rdi, %r14
	callq ferret_array_append
	movq %r14, %rdi
	movl $9, -1800(%rbp)
	leaq -1992(%rbp), %rax
	movq %rax, -1796(%rbp)
	leaq -1800(%rbp), %rsi
	movq %rdi, %r14
	callq ferret_array_append
	movq %r14, %rdi
	movl $16, -1760(%rbp)
	movq $str2, -1756(%rbp)
	leaq -1760(%rbp), %rsi
	movq %rdi, %r14
	callq ferret_array_append
	movq %r14, %rdi
	movl $9, -1720(%rbp)
	movq %r13, -1716(%rbp)
	leaq -1720(%rbp), %rsi
	movq %rdi, %r14
	callq ferret_array_append
	movq %r14, %rdi
	callq ferret_std_io_Println
	movl $5, %esi
	movl $36, %edi
	callq ferret_array_new
	movq %rax, %rdi
	movl $16, -1680(%rbp)
	movq $str5, -1676(%rbp)
	leaq -1680(%rbp), %rsi
	movq %rdi, %r14
	callq ferret_array_append
	movq %r14, %rdi
	movl $16, -1640(%rbp)
	movq $str1, -1636(%rbp)
	leaq -1640(%rbp), %rsi
	movq %rdi, %r14
	callq ferret_array_append
	movq %r14, %rdi
	movl $9, -1600(%rbp)
	leaq -1984(%rbp), %rax
	movq %rax, -1596(%rbp)
	leaq -1600(%rbp), %rsi
	movq %rdi, %r14
	callq ferret_array_append
	movq %r14, %rdi
	movl $16, -1560(%rbp)
	movq $str2, -1556(%rbp)
	leaq -1560(%rbp), %rsi
	movq %rdi, %r14
	callq ferret_array_append
	movq %r14, %rdi
	movl $9, -1520(%rbp)
	movq %r12, -1516(%rbp)
	leaq -1520(%rbp), %rsi
	movq %rdi, %r14
	callq ferret_array_append
	movq %r14, %rdi
	callq ferret_std_io_Println
	movl $5, %esi
	movl $36, %edi
	callq ferret_array_new
	movq %rax, %rdi
	movl $16, -1480(%rbp)
	movq $str6, -1476(%rbp)
	leaq -1480(%rbp), %rsi
	movq %rdi, %r14
	callq ferret_array_append
	movq %r14, %rdi
	movl $16, -1440(%rbp)
	movq $str1, -1436(%rbp)
	leaq -1440(%rbp), %rsi
	movq %rdi, %r14
	callq ferret_array_append
	movq %r14, %rdi
	movl $9, -1400(%rbp)
	leaq -1976(%rbp), %rax
	movq %rax, -1396(%rbp)
	leaq -1400(%rbp), %rsi
	movq %rdi, %r14
	callq ferret_array_append
	movq %r14, %rdi
	movl $16, -1360(%rbp)
	movq $str2, -1356(%rbp)
	leaq -1360(%rbp), %rsi
	movq %rdi, %r14
	callq ferret_array_append
	movq %r14, %rdi
	movl $9, -1320(%rbp)
	movq %rbx, -1316(%rbp)
	leaq -1320(%rbp), %rsi
	movq %rdi, %r14
	callq ferret_array_append
	movq %r14, %rdi
	callq ferret_std_io_Println
	movl $1, %esi
	movl $36, %edi
	callq ferret_array_new
	movq %rax, %rdi
	movl $16, -1280(%rbp)
	movq $str7, -1276(%rbp)
	leaq -1280(%rbp), %rsi
	movq %rdi, %r14
	callq ferret_array_append
	movq %r14, %rdi
	callq ferret_std_io_Println
	movl (%r13), %eax
	movl %eax, (%rbx)
	movq %r13, -1240(%rbp)
	movq $__typeid_2, -1232(%rbp)
	leaq -1240(%rbp), %rdx
	leaq -1240(%rbp), %rsi
	leaq str4(%rip), %rdi
	callq print_addrs
	movq %r12, -1224(%rbp)
	movq $__typeid_2, -1216(%rbp)
	leaq -1224(%rbp), %rdx
	leaq -1224(%rbp), %rsi
	leaq str5(%rip), %rdi
	callq print_addrs
	movq %rbx, -1208(%rbp)
	movq $__typeid_2, -1200(%rbp)
	leaq -1208(%rbp), %rdx
	leaq -1208(%rbp), %rsi
	leaq str6(%rip), %rdi
	callq print_addrs
	movl $1, %esi
	movl $36, %edi
	callq ferret_array_new
	movq %rax, %rdi
	movl $16, -1192(%rbp)
	movq $str7, -1188(%rbp)
	leaq -1192(%rbp), %rsi
	movq %rdi, %r14
	callq ferret_array_append
	movq %r14, %rdi
	callq ferret_std_io_Println
	movl $5, %esi
	movl $36, %edi
	callq ferret_array_new
	movq %rax, %rdi
	movl $16, -1152(%rbp)
	movq $str4, -1148(%rbp)
	leaq -1152(%rbp), %rsi
	movq %rdi, %r14
	callq ferret_array_append
	movq %r14, %rdi
	movl $16, -1112(%rbp)
	movq $str1, -1108(%rbp)
	leaq -1112(%rbp), %rsi
	movq %rdi, %r14
	callq ferret_array_append
	movq %r14, %rdi
	movl $9, -1072(%rbp)
	leaq -1992(%rbp), %rax
	movq %rax, -1068(%rbp)
	leaq -1072(%rbp), %rsi
	movq %rdi, %r14
	callq ferret_array_append
	movq %r14, %rdi
	movl $16, -1032(%rbp)
	movq $str2, -1028(%rbp)
	leaq -1032(%rbp), %rsi
	movq %rdi, %r14
	callq ferret_array_append
	movq %r14, %rdi
	movl $9, -992(%rbp)
	movq %r13, -988(%rbp)
	leaq -992(%rbp), %rsi
	movq %rdi, %r13
	callq ferret_array_append
	movq %r13, %rdi
	callq ferret_std_io_Println
	movl $5, %esi
	movl $36, %edi
	callq ferret_array_new
	movq %rax, %rdi
	movl $16, -952(%rbp)
	movq $str5, -948(%rbp)
	leaq -952(%rbp), %rsi
	movq %rdi, %r13
	callq ferret_array_append
	movq %r13, %rdi
	movl $16, -912(%rbp)
	movq $str1, -908(%rbp)
	leaq -912(%rbp), %rsi
	movq %rdi, %r13
	callq ferret_array_append
	movq %r13, %rdi
	movl $9, -872(%rbp)
	leaq -1984(%rbp), %rax
	movq %rax, -868(%rbp)
	leaq -872(%rbp), %rsi
	movq %rdi, %r13
	callq ferret_array_append
	movq %r13, %rdi
	movl $16, -832(%rbp)
	movq $str2, -828(%rbp)
	leaq -832(%rbp), %rsi
	movq %rdi, %r13
	callq ferret_array_append
	movq %r13, %rdi
	movl $9, -792(%rbp)
	movq %r12, -788(%rbp)
	leaq -792(%rbp), %rsi
	movq %rdi, %r12
	callq ferret_array_append
	movq %r12, %rdi
	callq ferret_std_io_Println
	movl $5, %esi
	movl $36, %edi
	callq ferret_array_new
	movq %rax, %rdi
	movl $16, -752(%rbp)
	movq $str6, -748(%rbp)
	leaq -752(%rbp), %rsi
	movq %rdi, %r12
	callq ferret_array_append
	movq %r12, %rdi
	movl $16, -712(%rbp)
	movq $str1, -708(%rbp)
	leaq -712(%rbp), %rsi
	movq %rdi, %r12
	callq ferret_array_append
	movq %r12, %rdi
	movl $9, -672(%rbp)
	leaq -1976(%rbp), %rax
	movq %rax, -668(%rbp)
	leaq -672(%rbp), %rsi
	movq %rdi, %r12
	callq ferret_array_append
	movq %r12, %rdi
	movl $16, -632(%rbp)
	movq $str2, -628(%rbp)
	leaq -632(%rbp), %rsi
	movq %rdi, %r12
	callq ferret_array_append
	movq %r12, %rdi
	movl $9, -592(%rbp)
	movq %rbx, -588(%rbp)
	leaq -592(%rbp), %rsi
	movq %rdi, %rbx
	callq ferret_array_append
	movq %rbx, %rdi
	callq ferret_std_io_Println
	callq make
	movq %rax, %rbx
	movq %rbx, -552(%rbp)
	movq $__typeid_1, -544(%rbp)
	leaq -552(%rbp), %rdx
	leaq -552(%rbp), %rsi
	leaq str8(%rip), %rdi
	callq print_addrs
	movl $1, %esi
	movl $36, %edi
	callq ferret_array_new
	movq %rax, %rdi
	movl 0(%rbx), %eax
	movl $2, -536(%rbp)
	movl %eax, -532(%rbp)
	leaq -536(%rbp), %rsi
	movq %rdi, %rbx
	callq ferret_array_append
	movq %rbx, %rdi
	callq ferret_std_io_Println
	movq $0, -496(%rbp)
	leaq str9(%rip), %rsi
	leaq -496(%rbp), %rdi
	callq ferret_string_assign
	movq $0, -488(%rbp)
	leaq str10(%rip), %rsi
	leaq -488(%rbp), %rdi
	callq ferret_string_assign
	movl $4, %esi
	movl $36, %edi
	callq ferret_array_new
	movq %rax, %rdi
	movq -496(%rbp), %rax
	movl $16, -480(%rbp)
	movq %rax, -476(%rbp)
	leaq -480(%rbp), %rsi
	movq %rdi, %rbx
	callq ferret_array_append
	movq %rbx, %rdi
	movl $9, -440(%rbp)
	leaq -496(%rbp), %rax
	movq %rax, -436(%rbp)
	leaq -440(%rbp), %rsi
	movq %rdi, %rbx
	callq ferret_array_append
	movq %rbx, %rdi
	movl $9, -400(%rbp)
	leaq -496(%rbp), %rax
	movq %rax, -396(%rbp)
	leaq -400(%rbp), %rsi
	movq %rdi, %rbx
	callq ferret_array_append
	movq %rbx, %rdi
	movq -496(%rbp), %rax
	movl $9, -360(%rbp)
	movq %rax, -356(%rbp)
	leaq -360(%rbp), %rsi
	movq %rdi, %rbx
	callq ferret_array_append
	movq %rbx, %rdi
	callq ferret_std_io_Println
	leaq str11(%rip), %rsi
	leaq -496(%rbp), %rdi
	callq ferret_string_assign
	movl $4, %esi
	movl $36, %edi
	callq ferret_array_new
	movq %rax, %rdi
	movq -496(%rbp), %rax
	movl $16, -320(%rbp)
	movq %rax, -316(%rbp)
	leaq -320(%rbp), %rsi
	movq %rdi, %rbx
	callq ferret_array_append
	movq %rbx, %rdi
	movl $9, -280(%rbp)
	leaq -496(%rbp), %rax
	movq %rax, -276(%rbp)
	leaq -280(%rbp), %rsi
	movq %rdi, %rbx
	callq ferret_array_append
	movq %rbx, %rdi
	movl $9, -240(%rbp)
	leaq -496(%rbp), %rax
	movq %rax, -236(%rbp)
	leaq -240(%rbp), %rsi
	movq %rdi, %rbx
	callq ferret_array_append
	movq %rbx, %rdi
	movq -496(%rbp), %rax
	movl $9, -200(%rbp)
	movq %rax, -196(%rbp)
	leaq -200(%rbp), %rsi
	movq %rdi, %rbx
	callq ferret_array_append
	movq %rbx, %rdi
	callq ferret_std_io_Println
	leaq str12(%rip), %rsi
	leaq -496(%rbp), %rdi
	callq ferret_string_assign
	movl $4, %esi
	movl $36, %edi
	callq ferret_array_new
	movq %rax, %rdi
	movq -496(%rbp), %rax
	movl $16, -160(%rbp)
	movq %rax, -156(%rbp)
	leaq -160(%rbp), %rsi
	movq %rdi, %rbx
	callq ferret_array_append
	movq %rbx, %rdi
	movl $9, -120(%rbp)
	leaq -496(%rbp), %rax
	movq %rax, -116(%rbp)
	leaq -120(%rbp), %rsi
	movq %rdi, %rbx
	callq ferret_array_append
	movq %rbx, %rdi
	movl $9, -80(%rbp)
	leaq -496(%rbp), %rax
	movq %rax, -76(%rbp)
	leaq -80(%rbp), %rsi
	movq %rdi, %rbx
	callq ferret_array_append
	movq %rbx, %rdi
	movq -496(%rbp), %rax
	movl $9, -40(%rbp)
	movq %rax, -36(%rbp)
	leaq -40(%rbp), %rsi
	movq %rdi, %rbx
	callq ferret_array_append
	movq %rbx, %rdi
	callq ferret_std_io_Println
	movl $0, %eax
	popq %r14
	popq %r13
	popq %r12
	popq %rbx
	leave
	ret
/* end function main */

