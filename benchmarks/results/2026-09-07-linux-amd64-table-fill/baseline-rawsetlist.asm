
/tmp/lunar-table-fill/baseline.test:     file format elf64-x86-64


Disassembly of section .init:

Disassembly of section .plt:

Disassembly of section .text:

000000000062bbe0 <github.com/mmcdole/lunar.(*tableObject).rawSetList>:
  62bbe0:	49 3b 66 10          	cmp    0x10(%r14),%rsp
  62bbe4:	0f 86 3a 02 00 00    	jbe    62be24 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x244>
  62bbea:	55                   	push   %rbp
  62bbeb:	48 89 e5             	mov    %rsp,%rbp
  62bbee:	48 83 ec 48          	sub    $0x48,%rsp
  62bbf2:	48 89 4c 24 68       	mov    %rcx,0x68(%rsp)
  62bbf7:	48 85 ff             	test   %rdi,%rdi
  62bbfa:	74 1c                	je     62bc18 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x38>
  62bbfc:	48 89 44 24 58       	mov    %rax,0x58(%rsp)
  62bc01:	8b 50 10             	mov    0x10(%rax),%edx
  62bc04:	4c 8d 42 01          	lea    0x1(%rdx),%r8
  62bc08:	4c 39 c3             	cmp    %r8,%rbx
  62bc0b:	75 11                	jne    62bc1e <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x3e>
  62bc0d:	83 78 38 00          	cmpl   $0x0,0x38(%rax)
  62bc11:	75 0b                	jne    62bc1e <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x3e>
  62bc13:	49 89 f8             	mov    %rdi,%r8
  62bc16:	eb 5e                	jmp    62bc76 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x96>
  62bc18:	48 83 c4 48          	add    $0x48,%rsp
  62bc1c:	5d                   	pop    %rbp
  62bc1d:	c3                   	ret
  62bc1e:	48 89 5c 24 60       	mov    %rbx,0x60(%rsp)
  62bc23:	48 89 7c 24 70       	mov    %rdi,0x70(%rsp)
  62bc28:	31 d2                	xor    %edx,%edx
  62bc2a:	eb 3c                	jmp    62bc68 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x88>
  62bc2c:	48 89 54 24 20       	mov    %rdx,0x20(%rsp)
  62bc31:	48 89 4c 24 38       	mov    %rcx,0x38(%rsp)
  62bc36:	48 8b 31             	mov    (%rcx),%rsi
  62bc39:	48 8b 79 08          	mov    0x8(%rcx),%rdi
  62bc3d:	48 01 d3             	add    %rdx,%rbx
  62bc40:	48 89 f1             	mov    %rsi,%rcx
  62bc43:	e8 f8 0a 00 00       	call   62c740 <github.com/mmcdole/lunar.(*tableObject).setInteger>
  62bc48:	48 8b 4c 24 38       	mov    0x38(%rsp),%rcx
  62bc4d:	48 83 c1 10          	add    $0x10,%rcx
  62bc51:	48 8b 54 24 20       	mov    0x20(%rsp),%rdx
  62bc56:	48 ff c2             	inc    %rdx
  62bc59:	48 8b 44 24 58       	mov    0x58(%rsp),%rax
  62bc5e:	48 8b 5c 24 60       	mov    0x60(%rsp),%rbx
  62bc63:	48 8b 7c 24 70       	mov    0x70(%rsp),%rdi
  62bc68:	48 39 d7             	cmp    %rdx,%rdi
  62bc6b:	7f bf                	jg     62bc2c <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x4c>
  62bc6d:	48 83 c4 48          	add    $0x48,%rsp
  62bc71:	5d                   	pop    %rbp
  62bc72:	c3                   	ret
  62bc73:	4c 89 d7             	mov    %r10,%rdi
  62bc76:	48 85 ff             	test   %rdi,%rdi
  62bc79:	7e 1b                	jle    62bc96 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0xb6>
  62bc7b:	4c 8d 4f ff          	lea    -0x1(%rdi),%r9
  62bc7f:	4d 89 ca             	mov    %r9,%r10
  62bc82:	49 c1 e1 04          	shl    $0x4,%r9
  62bc86:	4e 8b 0c 09          	mov    (%rcx,%r9,1),%r9
  62bc8a:	4c 39 0d 67 fe 36 00 	cmp    %r9,0x36fe67(%rip)        # 99baf8 <github.com/mmcdole/lunar.nilMarkerPointer>
  62bc91:	74 e0                	je     62bc73 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x93>
  62bc93:	48 85 ff             	test   %rdi,%rdi
  62bc96:	0f 84 e9 00 00 00    	je     62bd85 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x1a5>
  62bc9c:	0f 1f 40 00          	nopl   0x0(%rax)
  62bca0:	48 81 fa 00 00 00 04 	cmp    $0x4000000,%rdx
  62bca7:	0f 8f ce 00 00 00    	jg     62bd7b <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x19b>
  62bcad:	48 81 c2 00 00 00 fc 	add    $0xfffffffffc000000,%rdx
  62bcb4:	48 f7 da             	neg    %rdx
  62bcb7:	66 0f 1f 84 00 00 00 	nopw   0x0(%rax,%rax,1)
  62bcbe:	00 00 
  62bcc0:	48 39 d7             	cmp    %rdx,%rdi
  62bcc3:	0f 8f b2 00 00 00    	jg     62bd7b <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x19b>
  62bcc9:	48 89 7c 24 28       	mov    %rdi,0x28(%rsp)
  62bcce:	48 89 74 24 78       	mov    %rsi,0x78(%rsp)
  62bcd3:	48 89 4c 24 68       	mov    %rcx,0x68(%rsp)
  62bcd8:	8b 50 34             	mov    0x34(%rax),%edx
  62bcdb:	0f 1f 44 00 00       	nopl   0x0(%rax,%rax,1)
  62bce0:	83 fa 01             	cmp    $0x1,%edx
  62bce3:	76 23                	jbe    62bd08 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x128>
  62bce5:	8b 58 28             	mov    0x28(%rax),%ebx
  62bce8:	49 89 d8             	mov    %rbx,%r8
  62bceb:	49 c1 e8 02          	shr    $0x2,%r8
  62bcef:	44 39 c2             	cmp    %r8d,%edx
  62bcf2:	76 14                	jbe    62bd08 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x128>
  62bcf4:	39 50 30             	cmp    %edx,0x30(%rax)
  62bcf7:	73 0f                	jae    62bd08 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x128>
  62bcf9:	e8 02 dd ff ff       	call   629a00 <github.com/mmcdole/lunar.(*tableObject).rehashStore>
  62bcfe:	48 8b 44 24 58       	mov    0x58(%rsp),%rax
  62bd03:	48 8b 7c 24 28       	mov    0x28(%rsp),%rdi
  62bd08:	8b 48 10             	mov    0x10(%rax),%ecx
  62bd0b:	48 89 4c 24 30       	mov    %rcx,0x30(%rsp)
  62bd10:	48 8d 1c 0f          	lea    (%rdi,%rcx,1),%rbx
  62bd14:	e8 07 29 00 00       	call   62e620 <github.com/mmcdole/lunar.(*tableObject).growArray>
  62bd19:	48 8b 4c 24 58       	mov    0x58(%rsp),%rcx
  62bd1e:	8b 41 10             	mov    0x10(%rcx),%eax
  62bd21:	48 8b 51 08          	mov    0x8(%rcx),%rdx
  62bd25:	48 89 54 24 40       	mov    %rdx,0x40(%rsp)
  62bd2a:	be 10 00 00 00       	mov    $0x10,%esi
  62bd2f:	48 89 c3             	mov    %rax,%rbx
  62bd32:	48 f7 e6             	mul    %rsi
  62bd35:	0f 80 e2 00 00 00    	jo     62be1d <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x23d>
  62bd3b:	48 8b 54 24 40       	mov    0x40(%rsp),%rdx
  62bd40:	48 89 d6             	mov    %rdx,%rsi
  62bd43:	48 f7 da             	neg    %rdx
  62bd46:	48 39 d0             	cmp    %rdx,%rax
  62bd49:	0f 87 bf 00 00 00    	ja     62be0e <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x22e>
  62bd4f:	48 8b 44 24 78       	mov    0x78(%rsp),%rax
  62bd54:	48 8b 54 24 28       	mov    0x28(%rsp),%rdx
  62bd59:	0f 1f 80 00 00 00 00 	nopl   0x0(%rax)
  62bd60:	48 39 d0             	cmp    %rdx,%rax
  62bd63:	0f 82 a0 00 00 00    	jb     62be09 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x229>
  62bd69:	48 8b 44 24 68       	mov    0x68(%rsp),%rax
  62bd6e:	48 8b 7c 24 30       	mov    0x30(%rsp),%rdi
  62bd73:	45 31 c0             	xor    %r8d,%r8d
  62bd76:	45 31 c9             	xor    %r9d,%r9d
  62bd79:	eb 2a                	jmp    62bda5 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x1c5>
  62bd7b:	4c 89 c7             	mov    %r8,%rdi
  62bd7e:	66 90                	xchg   %ax,%ax
  62bd80:	e9 99 fe ff ff       	jmp    62bc1e <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x3e>
  62bd85:	48 83 c4 48          	add    $0x48,%rsp
  62bd89:	5d                   	pop    %rbp
  62bd8a:	c3                   	ret
  62bd8b:	4d 8d 51 01          	lea    0x1(%r9),%r10
  62bd8f:	48 83 c0 10          	add    $0x10,%rax
  62bd93:	90                   	nop
  62bd94:	49 ff c0             	inc    %r8
  62bd97:	4c 39 1d 5a fd 36 00 	cmp    %r11,0x36fd5a(%rip)        # 99baf8 <github.com/mmcdole/lunar.nilMarkerPointer>
  62bd9e:	4d 0f 44 d1          	cmove  %r9,%r10
  62bda2:	4d 89 d1             	mov    %r10,%r9
  62bda5:	49 39 d0             	cmp    %rdx,%r8
  62bda8:	7d 50                	jge    62bdfa <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x21a>
  62bdaa:	4e 8d 14 07          	lea    (%rdi,%r8,1),%r10
  62bdae:	4c 39 d3             	cmp    %r10,%rbx
  62bdb1:	76 51                	jbe    62be04 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x224>
  62bdb3:	4c 8b 18             	mov    (%rax),%r11
  62bdb6:	49 c1 e2 04          	shl    $0x4,%r10
  62bdba:	4e 8b 24 16          	mov    (%rsi,%r10,1),%r12
  62bdbe:	4c 8b 68 08          	mov    0x8(%rax),%r13
  62bdc2:	4d 39 dc             	cmp    %r11,%r12
  62bdc5:	75 07                	jne    62bdce <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x1ee>
  62bdc7:	4e 89 6c 16 08       	mov    %r13,0x8(%rsi,%r10,1)
  62bdcc:	eb bd                	jmp    62bd8b <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x1ab>
  62bdce:	83 3d 7b be 3b 00 00 	cmpl   $0x0,0x3bbe7b(%rip)        # 9e7c50 <runtime.writeBarrier>
  62bdd5:	74 18                	je     62bdef <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x20f>
  62bdd7:	4e 8b 24 16          	mov    (%rsi,%r10,1),%r12
  62bddb:	4d 89 df             	mov    %r11,%r15
  62bdde:	66 90                	xchg   %ax,%ax
  62bde0:	e8 fb 5c e6 ff       	call   491ae0 <runtime.gcWriteBarrier2>
  62bde5:	4d 89 3b             	mov    %r15,(%r11)
  62bde8:	4d 89 63 08          	mov    %r12,0x8(%r11)
  62bdec:	4d 89 fb             	mov    %r15,%r11
  62bdef:	4e 89 1c 16          	mov    %r11,(%rsi,%r10,1)
  62bdf3:	4e 89 6c 16 08       	mov    %r13,0x8(%rsi,%r10,1)
  62bdf8:	eb 91                	jmp    62bd8b <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x1ab>
  62bdfa:	4c 01 49 18          	add    %r9,0x18(%rcx)
  62bdfe:	48 83 c4 48          	add    $0x48,%rsp
  62be02:	5d                   	pop    %rbp
  62be03:	c3                   	ret
  62be04:	e8 77 60 e6 ff       	call   491e80 <runtime.panicBounds>
  62be09:	e8 72 60 e6 ff       	call   491e80 <runtime.panicBounds>
  62be0e:	48 85 f6             	test   %rsi,%rsi
  62be11:	74 05                	je     62be18 <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x238>
  62be13:	e8 68 84 e5 ff       	call   484280 <runtime.panicunsafeslicelen>
  62be18:	e8 03 85 e5 ff       	call   484320 <runtime.panicunsafeslicenilptr>
  62be1d:	48 8b 74 24 40       	mov    0x40(%rsp),%rsi
  62be22:	eb ea                	jmp    62be0e <github.com/mmcdole/lunar.(*tableObject).rawSetList+0x22e>
  62be24:	48 89 44 24 08       	mov    %rax,0x8(%rsp)
  62be29:	48 89 5c 24 10       	mov    %rbx,0x10(%rsp)
  62be2e:	48 89 4c 24 18       	mov    %rcx,0x18(%rsp)
  62be33:	48 89 7c 24 20       	mov    %rdi,0x20(%rsp)
  62be38:	48 89 74 24 28       	mov    %rsi,0x28(%rsp)
  62be3d:	0f 1f 00             	nopl   (%rax)
  62be40:	e8 9b 41 e6 ff       	call   48ffe0 <runtime.morestack_noctxt.abi0>
  62be45:	48 8b 44 24 08       	mov    0x8(%rsp),%rax
  62be4a:	48 8b 5c 24 10       	mov    0x10(%rsp),%rbx
  62be4f:	48 8b 4c 24 18       	mov    0x18(%rsp),%rcx
  62be54:	48 8b 7c 24 20       	mov    0x20(%rsp),%rdi
  62be59:	48 8b 74 24 28       	mov    0x28(%rsp),%rsi
  62be5e:	66 90                	xchg   %ax,%ax
  62be60:	e9 7b fd ff ff       	jmp    62bbe0 <github.com/mmcdole/lunar.(*tableObject).rawSetList>

Disassembly of section .fini:
