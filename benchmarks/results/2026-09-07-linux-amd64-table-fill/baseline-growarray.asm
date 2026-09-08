
/tmp/lunar-table-fill/baseline.test:     file format elf64-x86-64


Disassembly of section .init:

Disassembly of section .plt:

Disassembly of section .text:

000000000062e620 <github.com/mmcdole/lunar.(*tableObject).growArray>:
  62e620:	49 3b 66 10          	cmp    0x10(%r14),%rsp
  62e624:	0f 86 a5 04 00 00    	jbe    62eacf <github.com/mmcdole/lunar.(*tableObject).growArray+0x4af>
  62e62a:	55                   	push   %rbp
  62e62b:	48 89 e5             	mov    %rsp,%rbp
  62e62e:	48 83 ec 70          	sub    $0x70,%rsp
  62e632:	8b 50 10             	mov    0x10(%rax),%edx
  62e635:	48 8b 70 08          	mov    0x8(%rax),%rsi
  62e639:	48 89 c1             	mov    %rax,%rcx
  62e63c:	48 89 d0             	mov    %rdx,%rax
  62e63f:	41 b8 10 00 00 00    	mov    $0x10,%r8d
  62e645:	48 89 c7             	mov    %rax,%rdi
  62e648:	49 f7 e0             	mul    %r8
  62e64b:	0f 80 79 04 00 00    	jo     62eaca <github.com/mmcdole/lunar.(*tableObject).growArray+0x4aa>
  62e651:	48 89 f2             	mov    %rsi,%rdx
  62e654:	48 f7 de             	neg    %rsi
  62e657:	66 0f 1f 84 00 00 00 	nopw   0x0(%rax,%rax,1)
  62e65e:	00 00 
  62e660:	48 39 f0             	cmp    %rsi,%rax
  62e663:	0f 87 4f 04 00 00    	ja     62eab8 <github.com/mmcdole/lunar.(*tableObject).growArray+0x498>
  62e669:	48 89 4c 24 68       	mov    %rcx,0x68(%rsp)
  62e66e:	48 89 9c 24 88 00 00 	mov    %rbx,0x88(%rsp)
  62e675:	00 
  62e676:	48 89 7c 24 48       	mov    %rdi,0x48(%rsp)
  62e67b:	8b 71 14             	mov    0x14(%rcx),%esi
  62e67e:	66 90                	xchg   %ax,%ax
  62e680:	48 39 f3             	cmp    %rsi,%rbx
  62e683:	7f 1a                	jg     62e69f <github.com/mmcdole/lunar.(*tableObject).growArray+0x7f>
  62e685:	48 85 db             	test   %rbx,%rbx
  62e688:	0f 8c 17 04 00 00    	jl     62eaa5 <github.com/mmcdole/lunar.(*tableObject).growArray+0x485>
  62e68e:	48 39 f3             	cmp    %rsi,%rbx
  62e691:	0f 87 0e 04 00 00    	ja     62eaa5 <github.com/mmcdole/lunar.(*tableObject).growArray+0x485>
  62e697:	89 59 10             	mov    %ebx,0x10(%rcx)
  62e69a:	e9 2d 01 00 00       	jmp    62e7cc <github.com/mmcdole/lunar.(*tableObject).growArray+0x1ac>
  62e69f:	90                   	nop
  62e6a0:	48 81 fe 00 00 00 04 	cmp    $0x4000000,%rsi
  62e6a7:	0f 8f e1 03 00 00    	jg     62ea8e <github.com/mmcdole/lunar.(*tableObject).growArray+0x46e>
  62e6ad:	48 81 fb 00 00 00 04 	cmp    $0x4000000,%rbx
  62e6b4:	0f 8f d4 03 00 00    	jg     62ea8e <github.com/mmcdole/lunar.(*tableObject).growArray+0x46e>
  62e6ba:	48 85 f6             	test   %rsi,%rsi
  62e6bd:	41 b8 04 00 00 00    	mov    $0x4,%r8d
  62e6c3:	41 89 f1             	mov    %esi,%r9d
  62e6c6:	49 0f 44 f0          	cmove  %r8,%rsi
  62e6ca:	48 39 f3             	cmp    %rsi,%rbx
  62e6cd:	7e 2c                	jle    62e6fb <github.com/mmcdole/lunar.(*tableObject).growArray+0xdb>
  62e6cf:	48 83 fe 20          	cmp    $0x20,%rsi
  62e6d3:	7d 05                	jge    62e6da <github.com/mmcdole/lunar.(*tableObject).growArray+0xba>
  62e6d5:	48 01 f6             	add    %rsi,%rsi
  62e6d8:	eb f0                	jmp    62e6ca <github.com/mmcdole/lunar.(*tableObject).growArray+0xaa>
  62e6da:	49 89 f0             	mov    %rsi,%r8
  62e6dd:	48 d1 ee             	shr    $1,%rsi
  62e6e0:	4c 8d 96 00 00 00 fc 	lea    -0x4000000(%rsi),%r10
  62e6e7:	49 f7 da             	neg    %r10
  62e6ea:	4d 39 d0             	cmp    %r10,%r8
  62e6ed:	7e 07                	jle    62e6f6 <github.com/mmcdole/lunar.(*tableObject).growArray+0xd6>
  62e6ef:	be 00 00 00 04       	mov    $0x4000000,%esi
  62e6f4:	eb d4                	jmp    62e6ca <github.com/mmcdole/lunar.(*tableObject).growArray+0xaa>
  62e6f6:	4c 01 c6             	add    %r8,%rsi
  62e6f9:	eb cf                	jmp    62e6ca <github.com/mmcdole/lunar.(*tableObject).growArray+0xaa>
  62e6fb:	90                   	nop
  62e6fc:	41 b8 ff ff ff ff    	mov    $0xffffffff,%r8d
  62e702:	4c 39 c6             	cmp    %r8,%rsi
  62e705:	0f 87 70 03 00 00    	ja     62ea7b <github.com/mmcdole/lunar.(*tableObject).growArray+0x45b>
  62e70b:	48 89 74 24 38       	mov    %rsi,0x38(%rsp)
  62e710:	48 89 54 24 60       	mov    %rdx,0x60(%rsp)
  62e715:	44 89 4c 24 2c       	mov    %r9d,0x2c(%rsp)
  62e71a:	48 8d 05 df d1 0f 00 	lea    0xfd1df(%rip),%rax        # 72b900 <runtime.rodata+0x618e0>
  62e721:	48 89 f3             	mov    %rsi,%rbx
  62e724:	48 89 d9             	mov    %rbx,%rcx
  62e727:	e8 14 e1 e5 ff       	call   48c840 <runtime.makeslice>
  62e72c:	48 8b 94 24 88 00 00 	mov    0x88(%rsp),%rdx
  62e733:	00 
  62e734:	89 d1                	mov    %edx,%ecx
  62e736:	48 89 c3             	mov    %rax,%rbx
  62e739:	48 89 c8             	mov    %rcx,%rax
  62e73c:	be 10 00 00 00       	mov    $0x10,%esi
  62e741:	48 f7 e6             	mul    %rsi
  62e744:	0f 80 22 03 00 00    	jo     62ea6c <github.com/mmcdole/lunar.(*tableObject).growArray+0x44c>
  62e74a:	48 89 da             	mov    %rbx,%rdx
  62e74d:	48 f7 da             	neg    %rdx
  62e750:	48 39 d0             	cmp    %rdx,%rax
  62e753:	0f 87 13 03 00 00    	ja     62ea6c <github.com/mmcdole/lunar.(*tableObject).growArray+0x44c>
  62e759:	48 89 5c 24 58       	mov    %rbx,0x58(%rsp)
  62e75e:	48 8d 05 9b d1 0f 00 	lea    0xfd19b(%rip),%rax        # 72b900 <runtime.rodata+0x618e0>
  62e765:	48 8b 7c 24 60       	mov    0x60(%rsp),%rdi
  62e76a:	48 8b 74 24 48       	mov    0x48(%rsp),%rsi
  62e76f:	e8 2c a5 e5 ff       	call   488ca0 <runtime.typedslicecopy>
  62e774:	48 8b 94 24 88 00 00 	mov    0x88(%rsp),%rdx
  62e77b:	00 
  62e77c:	4c 8b 44 24 68       	mov    0x68(%rsp),%r8
  62e781:	41 89 50 10          	mov    %edx,0x10(%r8)
  62e785:	4c 8b 4c 24 38       	mov    0x38(%rsp),%r9
  62e78a:	45 89 48 14          	mov    %r9d,0x14(%r8)
  62e78e:	83 3d bb 94 3b 00 00 	cmpl   $0x0,0x3b94bb(%rip)        # 9e7c50 <runtime.writeBarrier>
  62e795:	75 07                	jne    62e79e <github.com/mmcdole/lunar.(*tableObject).growArray+0x17e>
  62e797:	4c 8b 4c 24 58       	mov    0x58(%rsp),%r9
  62e79c:	eb 15                	jmp    62e7b3 <github.com/mmcdole/lunar.(*tableObject).growArray+0x193>
  62e79e:	49 8b 70 08          	mov    0x8(%r8),%rsi
  62e7a2:	e8 39 33 e6 ff       	call   491ae0 <runtime.gcWriteBarrier2>
  62e7a7:	4c 8b 4c 24 58       	mov    0x58(%rsp),%r9
  62e7ac:	4d 89 0b             	mov    %r9,(%r11)
  62e7af:	49 89 73 08          	mov    %rsi,0x8(%r11)
  62e7b3:	4d 89 48 08          	mov    %r9,0x8(%r8)
  62e7b7:	4c 89 c1             	mov    %r8,%rcx
  62e7ba:	48 89 d3             	mov    %rdx,%rbx
  62e7bd:	8b 74 24 2c          	mov    0x2c(%rsp),%esi
  62e7c1:	48 8b 7c 24 48       	mov    0x48(%rsp),%rdi
  62e7c6:	41 b8 10 00 00 00    	mov    $0x10,%r8d
  62e7cc:	48 8b 01             	mov    (%rcx),%rax
  62e7cf:	48 85 c0             	test   %rax,%rax
  62e7d2:	74 2d                	je     62e801 <github.com/mmcdole/lunar.(*tableObject).growArray+0x1e1>
  62e7d4:	48 05 08 01 00 00    	add    $0x108,%rax
  62e7da:	8b 49 14             	mov    0x14(%rcx),%ecx
  62e7dd:	89 f3                	mov    %esi,%ebx
  62e7df:	bf 10 00 00 00       	mov    $0x10,%edi
  62e7e4:	e8 f7 34 f7 ff       	call   5a1ce0 <github.com/mmcdole/lunar.(*collectionControl).chargeCapacityGrowth>
  62e7e9:	48 8b 4c 24 68       	mov    0x68(%rsp),%rcx
  62e7ee:	48 8b 9c 24 88 00 00 	mov    0x88(%rsp),%rbx
  62e7f5:	00 
  62e7f6:	48 8b 7c 24 48       	mov    0x48(%rsp),%rdi
  62e7fb:	41 b8 10 00 00 00    	mov    $0x10,%r8d
  62e801:	8b 41 10             	mov    0x10(%rcx),%eax
  62e804:	48 8b 51 08          	mov    0x8(%rcx),%rdx
  62e808:	48 89 54 24 60       	mov    %rdx,0x60(%rsp)
  62e80d:	48 89 c6             	mov    %rax,%rsi
  62e810:	49 f7 e0             	mul    %r8
  62e813:	0f 80 4c 02 00 00    	jo     62ea65 <github.com/mmcdole/lunar.(*tableObject).growArray+0x445>
  62e819:	48 8b 54 24 60       	mov    0x60(%rsp),%rdx
  62e81e:	49 89 d0             	mov    %rdx,%r8
  62e821:	48 f7 da             	neg    %rdx
  62e824:	48 39 d0             	cmp    %rdx,%rax
  62e827:	0f 87 25 02 00 00    	ja     62ea52 <github.com/mmcdole/lunar.(*tableObject).growArray+0x432>
  62e82d:	48 89 f8             	mov    %rdi,%rax
  62e830:	eb 08                	jmp    62e83a <github.com/mmcdole/lunar.(*tableObject).growArray+0x21a>
  62e832:	4d 89 14 38          	mov    %r10,(%r8,%rdi,1)
  62e836:	48 8d 7a 01          	lea    0x1(%rdx),%rdi
  62e83a:	48 39 fb             	cmp    %rdi,%rbx
  62e83d:	7e 3f                	jle    62e87e <github.com/mmcdole/lunar.(*tableObject).growArray+0x25e>
  62e83f:	90                   	nop
  62e840:	48 39 f7             	cmp    %rsi,%rdi
  62e843:	0f 83 04 02 00 00    	jae    62ea4d <github.com/mmcdole/lunar.(*tableObject).growArray+0x42d>
  62e849:	48 89 fa             	mov    %rdi,%rdx
  62e84c:	48 c1 e7 04          	shl    $0x4,%rdi
  62e850:	4c 8b 0d 41 de 36 00 	mov    0x36de41(%rip),%r9        # 99c698 <github.com/mmcdole/lunar.nilSlot+0x8>
  62e857:	4c 8b 15 32 de 36 00 	mov    0x36de32(%rip),%r10        # 99c690 <github.com/mmcdole/lunar.nilSlot>
  62e85e:	4d 89 4c 38 08       	mov    %r9,0x8(%r8,%rdi,1)
  62e863:	83 3d e6 93 3b 00 00 	cmpl   $0x0,0x3b93e6(%rip)        # 9e7c50 <runtime.writeBarrier>
  62e86a:	74 c6                	je     62e832 <github.com/mmcdole/lunar.(*tableObject).growArray+0x212>
  62e86c:	4e 8b 0c 07          	mov    (%rdi,%r8,1),%r9
  62e870:	e8 6b 32 e6 ff       	call   491ae0 <runtime.gcWriteBarrier2>
  62e875:	4d 89 13             	mov    %r10,(%r11)
  62e878:	4d 89 4b 08          	mov    %r9,0x8(%r11)
  62e87c:	eb b4                	jmp    62e832 <github.com/mmcdole/lunar.(*tableObject).growArray+0x212>
  62e87e:	83 79 38 00          	cmpl   $0x0,0x38(%rcx)
  62e882:	74 0b                	je     62e88f <github.com/mmcdole/lunar.(*tableObject).growArray+0x26f>
  62e884:	48 89 74 24 48       	mov    %rsi,0x48(%rsp)
  62e889:	48 8d 50 01          	lea    0x1(%rax),%rdx
  62e88d:	eb 1a                	jmp    62e8a9 <github.com/mmcdole/lunar.(*tableObject).growArray+0x289>
  62e88f:	48 83 c4 70          	add    $0x70,%rsp
  62e893:	5d                   	pop    %rbp
  62e894:	c3                   	ret
  62e895:	48 ff c2             	inc    %rdx
  62e898:	48 89 c1             	mov    %rax,%rcx
  62e89b:	48 8b 9c 24 88 00 00 	mov    0x88(%rsp),%rbx
  62e8a2:	00 
  62e8a3:	4c 89 c6             	mov    %r8,%rsi
  62e8a6:	4d 89 c8             	mov    %r9,%r8
  62e8a9:	48 39 d3             	cmp    %rdx,%rbx
  62e8ac:	0f 8c 7d 01 00 00    	jl     62ea2f <github.com/mmcdole/lunar.(*tableObject).growArray+0x40f>
  62e8b2:	48 89 54 24 30       	mov    %rdx,0x30(%rsp)
  62e8b7:	0f 57 c0             	xorps  %xmm0,%xmm0
  62e8ba:	f2 48 0f 2a c2       	cvtsi2sd %rdx,%xmm0
  62e8bf:	90                   	nop
  62e8c0:	90                   	nop
  62e8c1:	0f 57 c9             	xorps  %xmm1,%xmm1
  62e8c4:	66 0f 2e c1          	ucomisd %xmm1,%xmm0
  62e8c8:	75 07                	jne    62e8d1 <github.com/mmcdole/lunar.(*tableObject).growArray+0x2b1>
  62e8ca:	7a 05                	jp     62e8d1 <github.com/mmcdole/lunar.(*tableObject).growArray+0x2b1>
  62e8cc:	0f 57 d2             	xorps  %xmm2,%xmm2
  62e8cf:	eb 03                	jmp    62e8d4 <github.com/mmcdole/lunar.(*tableObject).growArray+0x2b4>
  62e8d1:	0f 10 d0             	movups %xmm0,%xmm2
  62e8d4:	48 8d 41 20          	lea    0x20(%rcx),%rax
  62e8d8:	48 89 44 24 50       	mov    %rax,0x50(%rsp)
  62e8dd:	66 48 0f 7e c1       	movq   %xmm0,%rcx
  62e8e2:	66 48 0f 7e d7       	movq   %xmm2,%rdi
  62e8e7:	48 ba 15 7c 4a 7f b9 	movabs $0x9e3779b97f4a7c15,%rdx
  62e8ee:	79 37 9e 
  62e8f1:	48 31 fa             	xor    %rdi,%rdx
  62e8f4:	48 c1 ea 1e          	shr    $0x1e,%rdx
  62e8f8:	48 31 d7             	xor    %rdx,%rdi
  62e8fb:	48 ba 15 7c 4a 7f b9 	movabs $0x9e3779b97f4a7c15,%rdx
  62e902:	79 37 9e 
  62e905:	48 31 d7             	xor    %rdx,%rdi
  62e908:	48 ba b9 e5 e4 1c 6d 	movabs $0xbf58476d1ce4e5b9,%rdx
  62e90f:	47 58 bf 
  62e912:	48 0f af fa          	imul   %rdx,%rdi
  62e916:	48 89 fa             	mov    %rdi,%rdx
  62e919:	48 c1 ea 1b          	shr    $0x1b,%rdx
  62e91d:	48 31 d7             	xor    %rdx,%rdi
  62e920:	48 ba eb 11 31 13 bb 	movabs $0x94d049bb133111eb,%rdx
  62e927:	49 d0 94 
  62e92a:	48 0f af fa          	imul   %rdx,%rdi
  62e92e:	48 89 fa             	mov    %rdi,%rdx
  62e931:	48 c1 ea 1f          	shr    $0x1f,%rdx
  62e935:	48 31 d7             	xor    %rdx,%rdi
  62e938:	48 89 fa             	mov    %rdi,%rdx
  62e93b:	48 c1 ea 20          	shr    $0x20,%rdx
  62e93f:	48 31 d7             	xor    %rdx,%rdi
  62e942:	90                   	nop
  62e943:	85 ff                	test   %edi,%edi
  62e945:	ba 01 00 00 00       	mov    $0x1,%edx
  62e94a:	0f 44 fa             	cmove  %edx,%edi
  62e94d:	31 db                	xor    %ebx,%ebx
  62e94f:	e8 2c 16 00 00       	call   62ff80 <github.com/mmcdole/lunar.(*tableStore).find>
  62e954:	84 db                	test   %bl,%bl
  62e956:	75 19                	jne    62e971 <github.com/mmcdole/lunar.(*tableObject).growArray+0x351>
  62e958:	48 8b 44 24 68       	mov    0x68(%rsp),%rax
  62e95d:	48 8b 54 24 30       	mov    0x30(%rsp),%rdx
  62e962:	4c 8b 44 24 48       	mov    0x48(%rsp),%r8
  62e967:	4c 8b 4c 24 60       	mov    0x60(%rsp),%r9
  62e96c:	e9 24 ff ff ff       	jmp    62e895 <github.com/mmcdole/lunar.(*tableObject).growArray+0x275>
  62e971:	48 8b 4c 24 68       	mov    0x68(%rsp),%rcx
  62e976:	8b 51 28             	mov    0x28(%rcx),%edx
  62e979:	0f 1f 80 00 00 00 00 	nopl   0x0(%rax)
  62e980:	48 39 d0             	cmp    %rdx,%rax
  62e983:	0f 83 b1 00 00 00    	jae    62ea3a <github.com/mmcdole/lunar.(*tableObject).growArray+0x41a>
  62e989:	48 8b 49 20          	mov    0x20(%rcx),%rcx
  62e98d:	48 8d 14 80          	lea    (%rax,%rax,4),%rdx
  62e991:	48 8b 74 d1 18       	mov    0x18(%rcx,%rdx,8),%rsi
  62e996:	48 89 74 24 40       	mov    %rsi,0x40(%rsp)
  62e99b:	48 8b 4c d1 10       	mov    0x10(%rcx,%rdx,8),%rcx
  62e9a0:	48 89 4c 24 58       	mov    %rcx,0x58(%rsp)
  62e9a5:	48 89 c3             	mov    %rax,%rbx
  62e9a8:	48 8b 44 24 50       	mov    0x50(%rsp),%rax
  62e9ad:	e8 8e 14 00 00       	call   62fe40 <github.com/mmcdole/lunar.(*tableStore).deleteAt>
  62e9b2:	90                   	nop
  62e9b3:	48 8b 44 24 68       	mov    0x68(%rsp),%rax
  62e9b8:	83 78 38 00          	cmpl   $0x0,0x38(%rax)
  62e9bc:	75 04                	jne    62e9c2 <github.com/mmcdole/lunar.(*tableObject).growArray+0x3a2>
  62e9be:	c6 40 4c 00          	movb   $0x0,0x4c(%rax)
  62e9c2:	48 8b 54 24 30       	mov    0x30(%rsp),%rdx
  62e9c7:	48 8d 72 ff          	lea    -0x1(%rdx),%rsi
  62e9cb:	4c 8b 44 24 48       	mov    0x48(%rsp),%r8
  62e9d0:	49 39 f0             	cmp    %rsi,%r8
  62e9d3:	76 60                	jbe    62ea35 <github.com/mmcdole/lunar.(*tableObject).growArray+0x415>
  62e9d5:	48 c1 e6 04          	shl    $0x4,%rsi
  62e9d9:	4c 8b 4c 24 60       	mov    0x60(%rsp),%r9
  62e9de:	4d 8b 14 31          	mov    (%r9,%rsi,1),%r10
  62e9e2:	4c 8b 5c 24 58       	mov    0x58(%rsp),%r11
  62e9e7:	4d 39 da             	cmp    %r11,%r10
  62e9ea:	75 0c                	jne    62e9f8 <github.com/mmcdole/lunar.(*tableObject).growArray+0x3d8>
  62e9ec:	4c 8b 54 24 40       	mov    0x40(%rsp),%r10
  62e9f1:	4d 89 54 31 08       	mov    %r10,0x8(%r9,%rsi,1)
  62e9f6:	eb 2e                	jmp    62ea26 <github.com/mmcdole/lunar.(*tableObject).growArray+0x406>
  62e9f8:	83 3d 51 92 3b 00 00 	cmpl   $0x0,0x3b9251(%rip)        # 9e7c50 <runtime.writeBarrier>
  62e9ff:	90                   	nop
  62ea00:	74 16                	je     62ea18 <github.com/mmcdole/lunar.(*tableObject).growArray+0x3f8>
  62ea02:	4e 8b 14 0e          	mov    (%rsi,%r9,1),%r10
  62ea06:	4c 89 d9             	mov    %r11,%rcx
  62ea09:	e8 d2 30 e6 ff       	call   491ae0 <runtime.gcWriteBarrier2>
  62ea0e:	49 89 0b             	mov    %rcx,(%r11)
  62ea11:	4d 89 53 08          	mov    %r10,0x8(%r11)
  62ea15:	49 89 cb             	mov    %rcx,%r11
  62ea18:	4d 89 1c 31          	mov    %r11,(%r9,%rsi,1)
  62ea1c:	4c 8b 54 24 40       	mov    0x40(%rsp),%r10
  62ea21:	4d 89 54 31 08       	mov    %r10,0x8(%r9,%rsi,1)
  62ea26:	48 ff 40 18          	incq   0x18(%rax)
  62ea2a:	e9 66 fe ff ff       	jmp    62e895 <github.com/mmcdole/lunar.(*tableObject).growArray+0x275>
  62ea2f:	48 83 c4 70          	add    $0x70,%rsp
  62ea33:	5d                   	pop    %rbp
  62ea34:	c3                   	ret
  62ea35:	e8 46 34 e6 ff       	call   491e80 <runtime.panicBounds>
  62ea3a:	48 8d 05 bf ae 0b 00 	lea    0xbaebf(%rip),%rax        # 6e9900 <runtime.rodata+0x1f8e0>
  62ea41:	48 8d 1d 08 0e 13 00 	lea    0x130e08(%rip),%rbx        # 75f850 <github.com/mmcdole/lunar/benchmarks..stmp_17>
  62ea48:	e8 93 b4 e5 ff       	call   489ee0 <runtime.gopanic>
  62ea4d:	e8 2e 34 e6 ff       	call   491e80 <runtime.panicBounds>
  62ea52:	4d 85 c0             	test   %r8,%r8
  62ea55:	74 09                	je     62ea60 <github.com/mmcdole/lunar.(*tableObject).growArray+0x440>
  62ea57:	e8 24 58 e5 ff       	call   484280 <runtime.panicunsafeslicelen>
  62ea5c:	0f 1f 40 00          	nopl   0x0(%rax)
  62ea60:	e8 bb 58 e5 ff       	call   484320 <runtime.panicunsafeslicenilptr>
  62ea65:	4c 8b 44 24 60       	mov    0x60(%rsp),%r8
  62ea6a:	eb e6                	jmp    62ea52 <github.com/mmcdole/lunar.(*tableObject).growArray+0x432>
  62ea6c:	48 85 db             	test   %rbx,%rbx
  62ea6f:	74 05                	je     62ea76 <github.com/mmcdole/lunar.(*tableObject).growArray+0x456>
  62ea71:	e8 0a 58 e5 ff       	call   484280 <runtime.panicunsafeslicelen>
  62ea76:	e8 a5 58 e5 ff       	call   484320 <runtime.panicunsafeslicenilptr>
  62ea7b:	48 8d 05 7e ae 0b 00 	lea    0xbae7e(%rip),%rax        # 6e9900 <runtime.rodata+0x1f8e0>
  62ea82:	48 8d 1d b7 0d 13 00 	lea    0x130db7(%rip),%rbx        # 75f840 <github.com/mmcdole/lunar/benchmarks..stmp_16>
  62ea89:	e8 52 b4 e5 ff       	call   489ee0 <runtime.gopanic>
  62ea8e:	48 8d 05 6b ae 0b 00 	lea    0xbae6b(%rip),%rax        # 6e9900 <runtime.rodata+0x1f8e0>
  62ea95:	48 8d 1d a4 0d 13 00 	lea    0x130da4(%rip),%rbx        # 75f840 <github.com/mmcdole/lunar/benchmarks..stmp_16>
  62ea9c:	0f 1f 40 00          	nopl   0x0(%rax)
  62eaa0:	e8 3b b4 e5 ff       	call   489ee0 <runtime.gopanic>
  62eaa5:	48 8d 05 54 ae 0b 00 	lea    0xbae54(%rip),%rax        # 6e9900 <runtime.rodata+0x1f8e0>
  62eaac:	48 8d 1d 8d 0d 13 00 	lea    0x130d8d(%rip),%rbx        # 75f840 <github.com/mmcdole/lunar/benchmarks..stmp_16>
  62eab3:	e8 28 b4 e5 ff       	call   489ee0 <runtime.gopanic>
  62eab8:	48 85 d2             	test   %rdx,%rdx
  62eabb:	74 08                	je     62eac5 <github.com/mmcdole/lunar.(*tableObject).growArray+0x4a5>
  62eabd:	0f 1f 00             	nopl   (%rax)
  62eac0:	e8 bb 57 e5 ff       	call   484280 <runtime.panicunsafeslicelen>
  62eac5:	e8 56 58 e5 ff       	call   484320 <runtime.panicunsafeslicenilptr>
  62eaca:	48 89 f2             	mov    %rsi,%rdx
  62eacd:	eb e9                	jmp    62eab8 <github.com/mmcdole/lunar.(*tableObject).growArray+0x498>
  62eacf:	48 89 44 24 08       	mov    %rax,0x8(%rsp)
  62ead4:	48 89 5c 24 10       	mov    %rbx,0x10(%rsp)
  62ead9:	e8 02 15 e6 ff       	call   48ffe0 <runtime.morestack_noctxt.abi0>
  62eade:	48 8b 44 24 08       	mov    0x8(%rsp),%rax
  62eae3:	48 8b 5c 24 10       	mov    0x10(%rsp),%rbx
  62eae8:	e9 33 fb ff ff       	jmp    62e620 <github.com/mmcdole/lunar.(*tableObject).growArray>

Disassembly of section .fini:
