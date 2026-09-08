
/tmp/lunar-table-fill/candidate.test:     file format elf64-x86-64


Disassembly of section .init:

Disassembly of section .plt:

Disassembly of section .text:

0000000000488ca0 <runtime.typedslicecopy>:
  488ca0:	55                   	push   %rbp
  488ca1:	48 89 e5             	mov    %rsp,%rbp
  488ca4:	48 83 ec 30          	sub    $0x30,%rsp
  488ca8:	48 39 ce             	cmp    %rcx,%rsi
  488cab:	48 0f 4c ce          	cmovl  %rsi,%rcx
  488caf:	48 85 c9             	test   %rcx,%rcx
  488cb2:	74 78                	je     488d2c <runtime.typedslicecopy+0x8c>
  488cb4:	48 39 df             	cmp    %rbx,%rdi
  488cb7:	74 6a                	je     488d23 <runtime.typedslicecopy+0x83>
  488cb9:	48 89 4c 24 28       	mov    %rcx,0x28(%rsp)
  488cbe:	48 8b 10             	mov    (%rax),%rdx
  488cc1:	48 89 ce             	mov    %rcx,%rsi
  488cc4:	48 0f af f2          	imul   %rdx,%rsi
  488cc8:	80 3d 81 ff 55 00 00 	cmpb   $0x0,0x55ff81(%rip)        # 9e8c50 <runtime.writeBarrier>
  488ccf:	74 39                	je     488d0a <runtime.typedslicecopy+0x6a>
  488cd1:	48 89 5c 24 48       	mov    %rbx,0x48(%rsp)
  488cd6:	48 89 7c 24 58       	mov    %rdi,0x58(%rsp)
  488cdb:	48 89 74 24 20       	mov    %rsi,0x20(%rsp)
  488ce0:	48 89 f1             	mov    %rsi,%rcx
  488ce3:	48 29 d1             	sub    %rdx,%rcx
  488ce6:	48 03 48 08          	add    0x8(%rax),%rcx
  488cea:	48 89 c2             	mov    %rax,%rdx
  488ced:	48 89 d8             	mov    %rbx,%rax
  488cf0:	48 89 fb             	mov    %rdi,%rbx
  488cf3:	48 89 d7             	mov    %rdx,%rdi
  488cf6:	e8 05 c8 f9 ff       	call   425500 <runtime.bulkBarrierPreWrite>
  488cfb:	48 8b 5c 24 48       	mov    0x48(%rsp),%rbx
  488d00:	48 8b 74 24 20       	mov    0x20(%rsp),%rsi
  488d05:	48 8b 7c 24 58       	mov    0x58(%rsp),%rdi
  488d0a:	48 89 d8             	mov    %rbx,%rax
  488d0d:	48 89 fb             	mov    %rdi,%rbx
  488d10:	48 89 f1             	mov    %rsi,%rcx
  488d13:	e8 08 95 00 00       	call   492220 <runtime.memmove>
  488d18:	48 8b 44 24 28       	mov    0x28(%rsp),%rax
  488d1d:	48 83 c4 30          	add    $0x30,%rsp
  488d21:	5d                   	pop    %rbp
  488d22:	c3                   	ret
  488d23:	48 89 c8             	mov    %rcx,%rax
  488d26:	48 83 c4 30          	add    $0x30,%rsp
  488d2a:	5d                   	pop    %rbp
  488d2b:	c3                   	ret
  488d2c:	31 c0                	xor    %eax,%eax
  488d2e:	48 83 c4 30          	add    $0x30,%rsp
  488d32:	5d                   	pop    %rbp
  488d33:	c3                   	ret

Disassembly of section .fini:
