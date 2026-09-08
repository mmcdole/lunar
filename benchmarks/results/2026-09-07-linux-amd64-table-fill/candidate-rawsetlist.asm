
/tmp/lunar-table-fill/candidate.test:     file format elf64-x86-64


Disassembly of section .init:

Disassembly of section .plt:

Disassembly of section .text:

000000000062bbe0 <github.com/mmcdole/lunar.(*tableObject).rawSetList>:
  62bbe0:	49 3b 66 10          	cmp    0x10(%r14),%rsp
  62bbe4:	0f 86 62 04 00 00    	jbe    62c04c <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x46c>
  62bbea:	55                   	push   %rbp
  62bbeb:	48 89 e5             	mov    %rsp,%rbp
  62bbee:	48 83 ec 70          	sub    $0x70,%rsp
  62bbf2:	48 89 8c 24 90 00 00 	mov    %rcx,0x90(%rsp)
  62bbf9:	00 
  62bbfa:	48 85 ff             	test   %rdi,%rdi
  62bbfd:	74 1f                	je     62bc1e <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x3e>
  62bbff:	48 89 84 24 80 00 00 	mov    %rax,0x80(%rsp)
  62bc06:	00 
  62bc07:	8b 50 10             	mov    0x10(%rax),%edx
  62bc0a:	4c 8d 42 01          	lea    0x1(%rdx),%r8
  62bc0e:	4c 39 c3             	cmp    %r8,%rbx
  62bc11:	75 11                	jne    62bc24 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x44>
  62bc13:	83 78 38 00          	cmpl   $0x0,0x38(%rax)
  62bc17:	75 0b                	jne    62bc24 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x44>
  62bc19:	49 89 f8             	mov    %rdi,%r8
  62bc1c:	eb 70                	jmp    62bc8e <github.com/mmcdole/lunar.(*tableObject).rawSetList+0xae>
  62bc1e:	48 83 c4 70          	add    $0x70,%rsp
  62bc22:	5d                   	pop    %rbp
  62bc23:	c3                   	ret
  62bc24:	48 89 9c 24 88 00 00 	mov    %rbx,0x88(%rsp)
  62bc2b:	00 
  62bc2c:	48 89 bc 24 98 00 00 	mov    %rdi,0x98(%rsp)
  62bc33:	00 
  62bc34:	31 d2                	xor    %edx,%edx
  62bc36:	eb 48                	jmp    62bc80 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0xa0>
  62bc38:	48 89 54 24 30       	mov    %rdx,0x30(%rsp)
  62bc3d:	48 89 4c 24 68       	mov    %rcx,0x68(%rsp)
  62bc42:	48 8b 31             	mov    (%rcx),%rsi
  62bc45:	48 8b 79 08          	mov    0x8(%rcx),%rdi
  62bc49:	48 01 d3             	add    %rdx,%rbx
  62bc4c:	48 89 f1             	mov    %rsi,%rcx
  62bc4f:	e8 0c 0d 00 00       	call   62c960 <github.com/mmcdole/lunar.(*tableObject).setInteger>
  62bc54:	48 8b 4c 24 68       	mov    0x68(%rsp),%rcx
  62bc59:	48 83 c1 10          	add    $0x10,%rcx
  62bc5d:	48 8b 54 24 30       	mov    0x30(%rsp),%rdx
  62bc62:	48 ff c2             	inc    %rdx
  62bc65:	48 8b 84 24 80 00 00 	mov    0x80(%rsp),%rax
  62bc6c:	00 
  62bc6d:	48 8b 9c 24 88 00 00 	mov    0x88(%rsp),%rbx
  62bc74:	00 
  62bc75:	48 8b bc 24 98 00 00 	mov    0x98(%rsp),%rdi
  62bc7c:	00 
  62bc7d:	0f 1f 00             	nopl   (%rax)
  62bc80:	48 39 d7             	cmp    %rdx,%rdi
  62bc83:	7f b3                	jg     62bc38 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x58>
  62bc85:	48 83 c4 70          	add    $0x70,%rsp
  62bc89:	5d                   	pop    %rbp
  62bc8a:	c3                   	ret
  62bc8b:	4c 89 d7             	mov    %r10,%rdi
  62bc8e:	48 85 ff             	test   %rdi,%rdi
  62bc91:	7e 1b                	jle    62bcae <github.com/mmcdole/lunar.(*tableObject).rawSetList+0xce>
  62bc93:	4c 8d 4f ff          	lea    -0x1(%rdi),%r9
  62bc97:	4d 89 ca             	mov    %r9,%r10
  62bc9a:	49 c1 e1 04          	shl    $0x4,%r9
  62bc9e:	4e 8b 0c 09          	mov    (%rcx,%r9,1),%r9
  62bca2:	4c 39 0d 4f 0e 37 00 	cmp    %r9,0x370e4f(%rip)        # 99caf8 <github.com/mmcdole/lunar.nilMarkerPointer>
  62bca9:	74 e0                	je     62bc8b <github.com/mmcdole/lunar.(*tableObject).rawSetList+0xab>
  62bcab:	48 85 ff             	test   %rdi,%rdi
  62bcae:	0f 84 f1 00 00 00    	je     62bda5 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x1c5>
  62bcb4:	66 0f 1f 84 00 00 00 	nopw   0x0(%rax,%rax,1)
  62bcbb:	00 00 
  62bcbd:	0f 1f 00             	nopl   (%rax)
  62bcc0:	48 81 fa 00 00 00 04 	cmp    $0x4000000,%rdx
  62bcc7:	0f 8f d0 00 00 00    	jg     62bd9d <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x1bd>
  62bccd:	48 81 c2 00 00 00 fc 	add    $0xfffffffffc000000,%rdx
  62bcd4:	48 f7 da             	neg    %rdx
  62bcd7:	66 0f 1f 84 00 00 00 	nopw   0x0(%rax,%rax,1)
  62bcde:	00 00 
  62bce0:	48 39 d7             	cmp    %rdx,%rdi
  62bce3:	0f 8f b4 00 00 00    	jg     62bd9d <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x1bd>
  62bce9:	48 89 7c 24 40       	mov    %rdi,0x40(%rsp)
  62bcee:	48 89 8c 24 90 00 00 	mov    %rcx,0x90(%rsp)
  62bcf5:	00 
  62bcf6:	48 89 b4 24 a0 00 00 	mov    %rsi,0xa0(%rsp)
  62bcfd:	00 
  62bcfe:	8b 50 34             	mov    0x34(%rax),%edx
  62bd01:	83 fa 01             	cmp    $0x1,%edx
  62bd04:	76 36                	jbe    62bd3c <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x15c>
  62bd06:	8b 58 28             	mov    0x28(%rax),%ebx
  62bd09:	49 89 d8             	mov    %rbx,%r8
  62bd0c:	49 c1 e8 02          	shr    $0x2,%r8
  62bd10:	44 39 c2             	cmp    %r8d,%edx
  62bd13:	76 27                	jbe    62bd3c <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x15c>
  62bd15:	39 50 30             	cmp    %edx,0x30(%rax)
  62bd18:	73 22                	jae    62bd3c <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x15c>
  62bd1a:	e8 e1 dc ff ff       	call   629a00 <github.com/mmcdole/lunar.(*tableObject).rehashStore>
  62bd1f:	48 8b 84 24 80 00 00 	mov    0x80(%rsp),%rax
  62bd26:	00 
  62bd27:	48 8b 8c 24 90 00 00 	mov    0x90(%rsp),%rcx
  62bd2e:	00 
  62bd2f:	48 8b b4 24 a0 00 00 	mov    0xa0(%rsp),%rsi
  62bd36:	00 
  62bd37:	48 8b 7c 24 40       	mov    0x40(%rsp),%rdi
  62bd3c:	8b 50 10             	mov    0x10(%rax),%edx
  62bd3f:	48 89 54 24 58       	mov    %rdx,0x58(%rsp)
  62bd44:	4c 8d 04 17          	lea    (%rdi,%rdx,1),%r8
  62bd48:	44 8b 48 14          	mov    0x14(%rax),%r9d
  62bd4c:	4d 39 c1             	cmp    %r8,%r9
  62bd4f:	7d 34                	jge    62bd85 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x1a5>
  62bd51:	90                   	nop
  62bd52:	49 81 f9 00 00 00 04 	cmp    $0x4000000,%r9
  62bd59:	0f 8f d9 02 00 00    	jg     62c038 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x458>
  62bd5f:	90                   	nop
  62bd60:	49 81 f8 00 00 00 04 	cmp    $0x4000000,%r8
  62bd67:	0f 8f cb 02 00 00    	jg     62c038 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x458>
  62bd6d:	4d 85 c9             	test   %r9,%r9
  62bd70:	41 ba 04 00 00 00    	mov    $0x4,%r10d
  62bd76:	45 89 cb             	mov    %r9d,%r11d
  62bd79:	4d 0f 44 ca          	cmove  %r10,%r9
  62bd7d:	0f 1f 00             	nopl   (%rax)
  62bd80:	e9 20 01 00 00       	jmp    62bea5 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x2c5>
  62bd85:	4d 85 c0             	test   %r8,%r8
  62bd88:	0f 8c 01 01 00 00    	jl     62be8f <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x2af>
  62bd8e:	4d 39 c1             	cmp    %r8,%r9
  62bd91:	0f 82 f8 00 00 00    	jb     62be8f <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x2af>
  62bd97:	44 89 40 10          	mov    %r8d,0x10(%rax)
  62bd9b:	eb 0e                	jmp    62bdab <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x1cb>
  62bd9d:	4c 89 c7             	mov    %r8,%rdi
  62bda0:	e9 7f fe ff ff       	jmp    62bc24 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x44>
  62bda5:	48 83 c4 70          	add    $0x70,%rsp
  62bda9:	5d                   	pop    %rbp
  62bdaa:	c3                   	ret
  62bdab:	44 8b 40 10          	mov    0x10(%rax),%r8d
  62bdaf:	4c 8b 48 08          	mov    0x8(%rax),%r9
  62bdb3:	4c 89 c0             	mov    %r8,%rax
  62bdb6:	41 ba 10 00 00 00    	mov    $0x10,%r10d
  62bdbc:	49 f7 e2             	mul    %r10
  62bdbf:	90                   	nop
  62bdc0:	0f 80 c4 00 00 00    	jo     62be8a <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x2aa>
  62bdc6:	4c 89 ca             	mov    %r9,%rdx
  62bdc9:	49 f7 d9             	neg    %r9
  62bdcc:	4c 39 c8             	cmp    %r9,%rax
  62bdcf:	0f 87 a4 00 00 00    	ja     62be79 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x299>
  62bdd5:	4c 8b 4c 24 58       	mov    0x58(%rsp),%r9
  62bdda:	66 0f 1f 44 00 00    	nopw   0x0(%rax,%rax,1)
  62bde0:	4d 39 c8             	cmp    %r9,%r8
  62bde3:	0f 82 8b 00 00 00    	jb     62be74 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x294>
  62bde9:	4d 89 ca             	mov    %r9,%r10
  62bdec:	4d 29 c1             	sub    %r8,%r9
  62bdef:	4d 89 d3             	mov    %r10,%r11
  62bdf2:	49 c1 e2 04          	shl    $0x4,%r10
  62bdf6:	49 c1 f9 3f          	sar    $0x3f,%r9
  62bdfa:	4d 21 ca             	and    %r9,%r10
  62bdfd:	4d 29 d8             	sub    %r11,%r8
  62be00:	4c 01 d2             	add    %r10,%rdx
  62be03:	48 39 fe             	cmp    %rdi,%rsi
  62be06:	72 67                	jb     62be6f <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x28f>
  62be08:	4c 89 44 24 50       	mov    %r8,0x50(%rsp)
  62be0d:	48 89 54 24 60       	mov    %rdx,0x60(%rsp)
  62be12:	48 8d 05 e7 0a 10 00 	lea    0x100ae7(%rip),%rax        # 72c900 <runtime.rodata+0x618e0>
  62be19:	48 89 d3             	mov    %rdx,%rbx
  62be1c:	48 89 fe             	mov    %rdi,%rsi
  62be1f:	48 89 cf             	mov    %rcx,%rdi
  62be22:	4c 89 c1             	mov    %r8,%rcx
  62be25:	e8 76 ce e5 ff       	call   488ca0 <runtime.typedslicecopy>
  62be2a:	48 8b 54 24 60       	mov    0x60(%rsp),%rdx
  62be2f:	48 8b 4c 24 50       	mov    0x50(%rsp),%rcx
  62be34:	31 c0                	xor    %eax,%eax
  62be36:	eb 20                	jmp    62be58 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x278>
  62be38:	48 89 d3             	mov    %rdx,%rbx
  62be3b:	48 8b 1b             	mov    (%rbx),%rbx
  62be3e:	48 8d 70 01          	lea    0x1(%rax),%rsi
  62be42:	90                   	nop
  62be43:	48 83 c2 10          	add    $0x10,%rdx
  62be47:	48 ff c9             	dec    %rcx
  62be4a:	48 39 1d a7 0c 37 00 	cmp    %rbx,0x370ca7(%rip)        # 99caf8 <github.com/mmcdole/lunar.nilMarkerPointer>
  62be51:	48 0f 44 f0          	cmove  %rax,%rsi
  62be55:	48 89 f0             	mov    %rsi,%rax
  62be58:	48 85 c9             	test   %rcx,%rcx
  62be5b:	7f db                	jg     62be38 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x258>
  62be5d:	48 8b 8c 24 80 00 00 	mov    0x80(%rsp),%rcx
  62be64:	00 
  62be65:	48 01 41 18          	add    %rax,0x18(%rcx)
  62be69:	48 83 c4 70          	add    $0x70,%rsp
  62be6d:	5d                   	pop    %rbp
  62be6e:	c3                   	ret
  62be6f:	e8 0c 60 e6 ff       	call   491e80 <runtime.panicBounds>
  62be74:	e8 07 60 e6 ff       	call   491e80 <runtime.panicBounds>
  62be79:	48 85 d2             	test   %rdx,%rdx
  62be7c:	74 07                	je     62be85 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x2a5>
  62be7e:	66 90                	xchg   %ax,%ax
  62be80:	e8 fb 83 e5 ff       	call   484280 <runtime.panicunsafeslicelen>
  62be85:	e8 96 84 e5 ff       	call   484320 <runtime.panicunsafeslicenilptr>
  62be8a:	4c 89 ca             	mov    %r9,%rdx
  62be8d:	eb ea                	jmp    62be79 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x299>
  62be8f:	48 8d 05 6a ea 0b 00 	lea    0xbea6a(%rip),%rax        # 6ea900 <runtime.rodata+0x1f8e0>
  62be96:	48 8d 1d a3 49 13 00 	lea    0x1349a3(%rip),%rbx        # 760840 <github.com/mmcdole/lunar/benchmarks..stmp_16>
  62be9d:	0f 1f 00             	nopl   (%rax)
  62bea0:	e8 3b e0 e5 ff       	call   489ee0 <runtime.gopanic>
  62bea5:	4d 39 c1             	cmp    %r8,%r9
  62bea8:	7d 2d                	jge    62bed7 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x2f7>
  62beaa:	49 83 f9 20          	cmp    $0x20,%r9
  62beae:	7d 05                	jge    62beb5 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x2d5>
  62beb0:	4d 01 c9             	add    %r9,%r9
  62beb3:	eb f0                	jmp    62bea5 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x2c5>
  62beb5:	4d 89 ca             	mov    %r9,%r10
  62beb8:	49 d1 e9             	shr    $1,%r9
  62bebb:	4d 8d a1 00 00 00 fc 	lea    -0x4000000(%r9),%r12
  62bec2:	49 f7 dc             	neg    %r12
  62bec5:	4d 39 e2             	cmp    %r12,%r10
  62bec8:	7e 08                	jle    62bed2 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x2f2>
  62beca:	41 b9 00 00 00 04    	mov    $0x4000000,%r9d
  62bed0:	eb d3                	jmp    62bea5 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x2c5>
  62bed2:	4d 01 d1             	add    %r10,%r9
  62bed5:	eb ce                	jmp    62bea5 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x2c5>
  62bed7:	90                   	nop
  62bed8:	41 ba ff ff ff ff    	mov    $0xffffffff,%r10d
  62bede:	66 90                	xchg   %ax,%ax
  62bee0:	4d 39 d1             	cmp    %r10,%r9
  62bee3:	0f 87 3c 01 00 00    	ja     62c025 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x445>
  62bee9:	4c 89 44 24 38       	mov    %r8,0x38(%rsp)
  62beee:	4c 89 4c 24 48       	mov    %r9,0x48(%rsp)
  62bef3:	44 89 5c 24 2c       	mov    %r11d,0x2c(%rsp)
  62bef8:	48 8d 05 01 0a 10 00 	lea    0x100a01(%rip),%rax        # 72c900 <runtime.rodata+0x618e0>
  62beff:	4c 89 cb             	mov    %r9,%rbx
  62bf02:	48 89 d9             	mov    %rbx,%rcx
  62bf05:	e8 36 09 e6 ff       	call   48c840 <runtime.makeslice>
  62bf0a:	48 8b 54 24 38       	mov    0x38(%rsp),%rdx
  62bf0f:	89 d1                	mov    %edx,%ecx
  62bf11:	48 89 c3             	mov    %rax,%rbx
  62bf14:	b8 10 00 00 00       	mov    $0x10,%eax
  62bf19:	48 f7 e1             	mul    %rcx
  62bf1c:	0f 1f 40 00          	nopl   0x0(%rax)
  62bf20:	0f 80 ee 00 00 00    	jo     62c014 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x434>
  62bf26:	48 89 da             	mov    %rbx,%rdx
  62bf29:	48 f7 da             	neg    %rdx
  62bf2c:	48 39 d0             	cmp    %rdx,%rax
  62bf2f:	0f 87 df 00 00 00    	ja     62c014 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x434>
  62bf35:	48 8b 94 24 80 00 00 	mov    0x80(%rsp),%rdx
  62bf3c:	00 
  62bf3d:	8b 42 10             	mov    0x10(%rdx),%eax
  62bf40:	48 8b 7a 08          	mov    0x8(%rdx),%rdi
  62bf44:	41 b8 10 00 00 00    	mov    $0x10,%r8d
  62bf4a:	48 89 c6             	mov    %rax,%rsi
  62bf4d:	49 f7 e0             	mul    %r8
  62bf50:	0f 80 af 00 00 00    	jo     62c005 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x425>
  62bf56:	48 89 fa             	mov    %rdi,%rdx
  62bf59:	48 f7 da             	neg    %rdx
  62bf5c:	0f 1f 40 00          	nopl   0x0(%rax)
  62bf60:	48 39 d0             	cmp    %rdx,%rax
  62bf63:	0f 87 9c 00 00 00    	ja     62c005 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x425>
  62bf69:	48 89 5c 24 68       	mov    %rbx,0x68(%rsp)
  62bf6e:	48 8d 05 8b 09 10 00 	lea    0x10098b(%rip),%rax        # 72c900 <runtime.rodata+0x618e0>
  62bf75:	e8 26 cd e5 ff       	call   488ca0 <runtime.typedslicecopy>
  62bf7a:	48 8b 54 24 38       	mov    0x38(%rsp),%rdx
  62bf7f:	4c 8b 84 24 80 00 00 	mov    0x80(%rsp),%r8
  62bf86:	00 
  62bf87:	41 89 50 10          	mov    %edx,0x10(%r8)
  62bf8b:	48 8b 4c 24 48       	mov    0x48(%rsp),%rcx
  62bf90:	41 89 48 14          	mov    %ecx,0x14(%r8)
  62bf94:	83 3d b5 cc 3b 00 00 	cmpl   $0x0,0x3bccb5(%rip)        # 9e8c50 <runtime.writeBarrier>
  62bf9b:	75 07                	jne    62bfa4 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x3c4>
  62bf9d:	4c 8b 4c 24 68       	mov    0x68(%rsp),%r9
  62bfa2:	eb 15                	jmp    62bfb9 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x3d9>
  62bfa4:	49 8b 50 08          	mov    0x8(%r8),%rdx
  62bfa8:	e8 33 5b e6 ff       	call   491ae0 <runtime.gcWriteBarrier2>
  62bfad:	4c 8b 4c 24 68       	mov    0x68(%rsp),%r9
  62bfb2:	4d 89 0b             	mov    %r9,(%r11)
  62bfb5:	49 89 53 08          	mov    %rdx,0x8(%r11)
  62bfb9:	4d 89 48 08          	mov    %r9,0x8(%r8)
  62bfbd:	49 8b 00             	mov    (%r8),%rax
  62bfc0:	48 85 c0             	test   %rax,%rax
  62bfc3:	74 1c                	je     62bfe1 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x401>
  62bfc5:	48 05 08 01 00 00    	add    $0x108,%rax
  62bfcb:	8b 5c 24 2c          	mov    0x2c(%rsp),%ebx
  62bfcf:	bf 10 00 00 00       	mov    $0x10,%edi
  62bfd4:	e8 07 5d f7 ff       	call   5a1ce0 <github.com/mmcdole/lunar.(*collectionControl).chargeCapacityGrowth>
  62bfd9:	4c 8b 84 24 80 00 00 	mov    0x80(%rsp),%r8
  62bfe0:	00 
  62bfe1:	4c 89 c0             	mov    %r8,%rax
  62bfe4:	48 8b 8c 24 90 00 00 	mov    0x90(%rsp),%rcx
  62bfeb:	00 
  62bfec:	48 8b 54 24 58       	mov    0x58(%rsp),%rdx
  62bff1:	48 8b b4 24 a0 00 00 	mov    0xa0(%rsp),%rsi
  62bff8:	00 
  62bff9:	48 8b 7c 24 40       	mov    0x40(%rsp),%rdi
  62bffe:	66 90                	xchg   %ax,%ax
  62c000:	e9 a6 fd ff ff       	jmp    62bdab <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x1cb>
  62c005:	48 85 ff             	test   %rdi,%rdi
  62c008:	74 05                	je     62c00f <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x42f>
  62c00a:	e8 71 82 e5 ff       	call   484280 <runtime.panicunsafeslicelen>
  62c00f:	e8 0c 83 e5 ff       	call   484320 <runtime.panicunsafeslicenilptr>
  62c014:	48 85 db             	test   %rbx,%rbx
  62c017:	74 07                	je     62c020 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x440>
  62c019:	e8 62 82 e5 ff       	call   484280 <runtime.panicunsafeslicelen>
  62c01e:	66 90                	xchg   %ax,%ax
  62c020:	e8 fb 82 e5 ff       	call   484320 <runtime.panicunsafeslicenilptr>
  62c025:	48 8d 05 d4 e8 0b 00 	lea    0xbe8d4(%rip),%rax        # 6ea900 <runtime.rodata+0x1f8e0>
  62c02c:	48 8d 1d 0d 48 13 00 	lea    0x13480d(%rip),%rbx        # 760840 <github.com/mmcdole/lunar/benchmarks..stmp_16>
  62c033:	e8 a8 de e5 ff       	call   489ee0 <runtime.gopanic>
  62c038:	48 8d 05 c1 e8 0b 00 	lea    0xbe8c1(%rip),%rax        # 6ea900 <runtime.rodata+0x1f8e0>
  62c03f:	48 8d 1d fa 47 13 00 	lea    0x1347fa(%rip),%rbx        # 760840 <github.com/mmcdole/lunar/benchmarks..stmp_16>
  62c046:	e8 95 de e5 ff       	call   489ee0 <runtime.gopanic>
  62c04b:	90                   	nop
  62c04c:	48 89 44 24 08       	mov    %rax,0x8(%rsp)
  62c051:	48 89 5c 24 10       	mov    %rbx,0x10(%rsp)
  62c056:	48 89 4c 24 18       	mov    %rcx,0x18(%rsp)
  62c05b:	48 89 7c 24 20       	mov    %rdi,0x20(%rsp)
  62c060:	48 89 74 24 28       	mov    %rsi,0x28(%rsp)
  62c065:	e8 76 3f e6 ff       	call   48ffe0 <runtime.morestack_noctxt.abi0>
  62c06a:	48 8b 44 24 08       	mov    0x8(%rsp),%rax
  62c06f:	48 8b 5c 24 10       	mov    0x10(%rsp),%rbx
  62c074:	48 8b 4c 24 18       	mov    0x18(%rsp),%rcx
  62c079:	48 8b 7c 24 20       	mov    0x20(%rsp),%rdi
  62c07e:	48 8b 74 24 28       	mov    0x28(%rsp),%rsi
  62c083:	e9 58 fb ff ff       	jmp    62bbe0 <github.com/mmcdole/lunar.(*tableObject).rawSetList>

Disassembly of section .fini:
