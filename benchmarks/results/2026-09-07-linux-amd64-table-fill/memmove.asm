
/tmp/lunar-table-fill/candidate.test:     file format elf64-x86-64


Disassembly of section .init:

Disassembly of section .plt:

Disassembly of section .text:

0000000000492220 <runtime.memmove>:
  492220:	48 89 c7             	mov    %rax,%rdi
  492223:	48 89 de             	mov    %rbx,%rsi
  492226:	48 89 cb             	mov    %rcx,%rbx
  492229:	48 85 db             	test   %rbx,%rbx
  49222c:	0f 84 1b 01 00 00    	je     49234d <runtime.memmove+0x12d>
  492232:	48 83 fb 02          	cmp    $0x2,%rbx
  492236:	0f 86 04 01 00 00    	jbe    492340 <runtime.memmove+0x120>
  49223c:	48 83 fb 04          	cmp    $0x4,%rbx
  492240:	0f 82 0d 01 00 00    	jb     492353 <runtime.memmove+0x133>
  492246:	0f 86 02 01 00 00    	jbe    49234e <runtime.memmove+0x12e>
  49224c:	48 83 fb 08          	cmp    $0x8,%rbx
  492250:	0f 82 0a 01 00 00    	jb     492360 <runtime.memmove+0x140>
  492256:	0f 84 11 01 00 00    	je     49236d <runtime.memmove+0x14d>
  49225c:	48 83 fb 10          	cmp    $0x10,%rbx
  492260:	0f 86 0e 01 00 00    	jbe    492374 <runtime.memmove+0x154>
  492266:	48 83 fb 20          	cmp    $0x20,%rbx
  49226a:	0f 86 15 01 00 00    	jbe    492385 <runtime.memmove+0x165>
  492270:	48 83 fb 40          	cmp    $0x40,%rbx
  492274:	0f 86 20 01 00 00    	jbe    49239a <runtime.memmove+0x17a>
  49227a:	48 81 fb 80 00 00 00 	cmp    $0x80,%rbx
  492281:	0f 86 3e 01 00 00    	jbe    4923c5 <runtime.memmove+0x1a5>
  492287:	48 81 fb 00 01 00 00 	cmp    $0x100,%rbx
  49228e:	0f 86 88 01 00 00    	jbe    49241c <runtime.memmove+0x1fc>
  492294:	8a 05 3b 66 55 00    	mov    0x55663b(%rip),%al        # 9e88d5 <runtime.memmoveBits>
  49229a:	3c 01                	cmp    $0x1,%al
  49229c:	0f 84 48 03 00 00    	je     4925ea <runtime.memmove+0x3ca>
  4922a2:	48 39 fe             	cmp    %rdi,%rsi
  4922a5:	76 55                	jbe    4922fc <runtime.memmove+0xdc>
  4922a7:	48 81 fb 00 08 00 00 	cmp    $0x800,%rbx
  4922ae:	7c 0d                	jl     4922bd <runtime.memmove+0x9d>
  4922b0:	48 f7 c7 0f 00 00 00 	test   $0xf,%rdi
  4922b7:	75 04                	jne    4922bd <runtime.memmove+0x9d>
  4922b9:	a8 02                	test   $0x2,%al
  4922bb:	75 26                	jne    4922e3 <runtime.memmove+0xc3>
  4922bd:	a8 01                	test   $0x1,%al
  4922bf:	0f 85 25 03 00 00    	jne    4925ea <runtime.memmove+0x3ca>
  4922c5:	48 81 fb 00 08 00 00 	cmp    $0x800,%rbx
  4922cc:	0f 86 0e 02 00 00    	jbe    4924e0 <runtime.memmove+0x2c0>
  4922d2:	89 f0                	mov    %esi,%eax
  4922d4:	09 f8                	or     %edi,%eax
  4922d6:	a9 07 00 00 00       	test   $0x7,%eax
  4922db:	74 06                	je     4922e3 <runtime.memmove+0xc3>
  4922dd:	48 89 d9             	mov    %rbx,%rcx
  4922e0:	f3 a4                	rep movsb (%rsi),(%rdi)
  4922e2:	c3                   	ret
  4922e3:	48 8b 44 1e f8       	mov    -0x8(%rsi,%rbx,1),%rax
  4922e8:	48 8d 54 1f f8       	lea    -0x8(%rdi,%rbx,1),%rdx
  4922ed:	48 8d 4b ff          	lea    -0x1(%rbx),%rcx
  4922f1:	48 c1 e9 03          	shr    $0x3,%rcx
  4922f5:	f3 48 a5             	rep movsq (%rsi),(%rdi)
  4922f8:	48 89 02             	mov    %rax,(%rdx)
  4922fb:	c3                   	ret
  4922fc:	48 89 f1             	mov    %rsi,%rcx
  4922ff:	48 01 d9             	add    %rbx,%rcx
  492302:	48 39 f9             	cmp    %rdi,%rcx
  492305:	76 a0                	jbe    4922a7 <runtime.memmove+0x87>
  492307:	a8 01                	test   $0x1,%al
  492309:	0f 85 db 02 00 00    	jne    4925ea <runtime.memmove+0x3ca>
  49230f:	48 01 df             	add    %rbx,%rdi
  492312:	48 01 de             	add    %rbx,%rsi
  492315:	fd                   	std
  492316:	48 89 d9             	mov    %rbx,%rcx
  492319:	48 c1 e9 03          	shr    $0x3,%rcx
  49231d:	48 83 e3 07          	and    $0x7,%rbx
  492321:	48 83 ef 08          	sub    $0x8,%rdi
  492325:	48 83 ee 08          	sub    $0x8,%rsi
  492329:	f3 48 a5             	rep movsq (%rsi),(%rdi)
  49232c:	fc                   	cld
  49232d:	48 83 c7 08          	add    $0x8,%rdi
  492331:	48 83 c6 08          	add    $0x8,%rsi
  492335:	48 29 df             	sub    %rbx,%rdi
  492338:	48 29 de             	sub    %rbx,%rsi
  49233b:	e9 e9 fe ff ff       	jmp    492229 <runtime.memmove+0x9>
  492340:	8a 06                	mov    (%rsi),%al
  492342:	8a 4c 1e ff          	mov    -0x1(%rsi,%rbx,1),%cl
  492346:	88 07                	mov    %al,(%rdi)
  492348:	88 4c 1f ff          	mov    %cl,-0x1(%rdi,%rbx,1)
  49234c:	c3                   	ret
  49234d:	c3                   	ret
  49234e:	8b 06                	mov    (%rsi),%eax
  492350:	89 07                	mov    %eax,(%rdi)
  492352:	c3                   	ret
  492353:	66 8b 06             	mov    (%rsi),%ax
  492356:	8a 4e 02             	mov    0x2(%rsi),%cl
  492359:	66 89 07             	mov    %ax,(%rdi)
  49235c:	88 4f 02             	mov    %cl,0x2(%rdi)
  49235f:	c3                   	ret
  492360:	8b 06                	mov    (%rsi),%eax
  492362:	8b 4c 1e fc          	mov    -0x4(%rsi,%rbx,1),%ecx
  492366:	89 07                	mov    %eax,(%rdi)
  492368:	89 4c 1f fc          	mov    %ecx,-0x4(%rdi,%rbx,1)
  49236c:	c3                   	ret
  49236d:	48 8b 06             	mov    (%rsi),%rax
  492370:	48 89 07             	mov    %rax,(%rdi)
  492373:	c3                   	ret
  492374:	48 8b 06             	mov    (%rsi),%rax
  492377:	48 8b 4c 1e f8       	mov    -0x8(%rsi,%rbx,1),%rcx
  49237c:	48 89 07             	mov    %rax,(%rdi)
  49237f:	48 89 4c 1f f8       	mov    %rcx,-0x8(%rdi,%rbx,1)
  492384:	c3                   	ret
  492385:	f3 0f 6f 06          	movdqu (%rsi),%xmm0
  492389:	f3 0f 6f 4c 1e f0    	movdqu -0x10(%rsi,%rbx,1),%xmm1
  49238f:	f3 0f 7f 07          	movdqu %xmm0,(%rdi)
  492393:	f3 0f 7f 4c 1f f0    	movdqu %xmm1,-0x10(%rdi,%rbx,1)
  492399:	c3                   	ret
  49239a:	f3 0f 6f 06          	movdqu (%rsi),%xmm0
  49239e:	f3 0f 6f 4e 10       	movdqu 0x10(%rsi),%xmm1
  4923a3:	f3 0f 6f 54 1e e0    	movdqu -0x20(%rsi,%rbx,1),%xmm2
  4923a9:	f3 0f 6f 5c 1e f0    	movdqu -0x10(%rsi,%rbx,1),%xmm3
  4923af:	f3 0f 7f 07          	movdqu %xmm0,(%rdi)
  4923b3:	f3 0f 7f 4f 10       	movdqu %xmm1,0x10(%rdi)
  4923b8:	f3 0f 7f 54 1f e0    	movdqu %xmm2,-0x20(%rdi,%rbx,1)
  4923be:	f3 0f 7f 5c 1f f0    	movdqu %xmm3,-0x10(%rdi,%rbx,1)
  4923c4:	c3                   	ret
  4923c5:	f3 0f 6f 06          	movdqu (%rsi),%xmm0
  4923c9:	f3 0f 6f 4e 10       	movdqu 0x10(%rsi),%xmm1
  4923ce:	f3 0f 6f 56 20       	movdqu 0x20(%rsi),%xmm2
  4923d3:	f3 0f 6f 5e 30       	movdqu 0x30(%rsi),%xmm3
  4923d8:	f3 0f 6f 64 1e c0    	movdqu -0x40(%rsi,%rbx,1),%xmm4
  4923de:	f3 0f 6f 6c 1e d0    	movdqu -0x30(%rsi,%rbx,1),%xmm5
  4923e4:	f3 0f 6f 74 1e e0    	movdqu -0x20(%rsi,%rbx,1),%xmm6
  4923ea:	f3 0f 6f 7c 1e f0    	movdqu -0x10(%rsi,%rbx,1),%xmm7
  4923f0:	f3 0f 7f 07          	movdqu %xmm0,(%rdi)
  4923f4:	f3 0f 7f 4f 10       	movdqu %xmm1,0x10(%rdi)
  4923f9:	f3 0f 7f 57 20       	movdqu %xmm2,0x20(%rdi)
  4923fe:	f3 0f 7f 5f 30       	movdqu %xmm3,0x30(%rdi)
  492403:	f3 0f 7f 64 1f c0    	movdqu %xmm4,-0x40(%rdi,%rbx,1)
  492409:	f3 0f 7f 6c 1f d0    	movdqu %xmm5,-0x30(%rdi,%rbx,1)
  49240f:	f3 0f 7f 74 1f e0    	movdqu %xmm6,-0x20(%rdi,%rbx,1)
  492415:	f3 0f 7f 7c 1f f0    	movdqu %xmm7,-0x10(%rdi,%rbx,1)
  49241b:	c3                   	ret
  49241c:	f3 0f 6f 06          	movdqu (%rsi),%xmm0
  492420:	f3 0f 6f 4e 10       	movdqu 0x10(%rsi),%xmm1
  492425:	f3 0f 6f 56 20       	movdqu 0x20(%rsi),%xmm2
  49242a:	f3 0f 6f 5e 30       	movdqu 0x30(%rsi),%xmm3
  49242f:	f3 0f 6f 66 40       	movdqu 0x40(%rsi),%xmm4
  492434:	f3 0f 6f 6e 50       	movdqu 0x50(%rsi),%xmm5
  492439:	f3 0f 6f 76 60       	movdqu 0x60(%rsi),%xmm6
  49243e:	f3 0f 6f 7e 70       	movdqu 0x70(%rsi),%xmm7
  492443:	f3 44 0f 6f 44 1e 80 	movdqu -0x80(%rsi,%rbx,1),%xmm8
  49244a:	f3 44 0f 6f 4c 1e 90 	movdqu -0x70(%rsi,%rbx,1),%xmm9
  492451:	f3 44 0f 6f 54 1e a0 	movdqu -0x60(%rsi,%rbx,1),%xmm10
  492458:	f3 44 0f 6f 5c 1e b0 	movdqu -0x50(%rsi,%rbx,1),%xmm11
  49245f:	f3 44 0f 6f 64 1e c0 	movdqu -0x40(%rsi,%rbx,1),%xmm12
  492466:	f3 44 0f 6f 6c 1e d0 	movdqu -0x30(%rsi,%rbx,1),%xmm13
  49246d:	f3 44 0f 6f 74 1e e0 	movdqu -0x20(%rsi,%rbx,1),%xmm14
  492474:	f3 44 0f 6f 7c 1e f0 	movdqu -0x10(%rsi,%rbx,1),%xmm15
  49247b:	f3 0f 7f 07          	movdqu %xmm0,(%rdi)
  49247f:	f3 0f 7f 4f 10       	movdqu %xmm1,0x10(%rdi)
  492484:	f3 0f 7f 57 20       	movdqu %xmm2,0x20(%rdi)
  492489:	f3 0f 7f 5f 30       	movdqu %xmm3,0x30(%rdi)
  49248e:	f3 0f 7f 67 40       	movdqu %xmm4,0x40(%rdi)
  492493:	f3 0f 7f 6f 50       	movdqu %xmm5,0x50(%rdi)
  492498:	f3 0f 7f 77 60       	movdqu %xmm6,0x60(%rdi)
  49249d:	f3 0f 7f 7f 70       	movdqu %xmm7,0x70(%rdi)
  4924a2:	f3 44 0f 7f 44 1f 80 	movdqu %xmm8,-0x80(%rdi,%rbx,1)
  4924a9:	f3 44 0f 7f 4c 1f 90 	movdqu %xmm9,-0x70(%rdi,%rbx,1)
  4924b0:	f3 44 0f 7f 54 1f a0 	movdqu %xmm10,-0x60(%rdi,%rbx,1)
  4924b7:	f3 44 0f 7f 5c 1f b0 	movdqu %xmm11,-0x50(%rdi,%rbx,1)
  4924be:	f3 44 0f 7f 64 1f c0 	movdqu %xmm12,-0x40(%rdi,%rbx,1)
  4924c5:	f3 44 0f 7f 6c 1f d0 	movdqu %xmm13,-0x30(%rdi,%rbx,1)
  4924cc:	f3 44 0f 7f 74 1f e0 	movdqu %xmm14,-0x20(%rdi,%rbx,1)
  4924d3:	f3 44 0f 7f 7c 1f f0 	movdqu %xmm15,-0x10(%rdi,%rbx,1)
  4924da:	66 45 0f ef ff       	pxor   %xmm15,%xmm15
  4924df:	c3                   	ret
  4924e0:	48 81 eb 00 01 00 00 	sub    $0x100,%rbx
  4924e7:	f3 0f 6f 06          	movdqu (%rsi),%xmm0
  4924eb:	f3 0f 6f 4e 10       	movdqu 0x10(%rsi),%xmm1
  4924f0:	f3 0f 6f 56 20       	movdqu 0x20(%rsi),%xmm2
  4924f5:	f3 0f 6f 5e 30       	movdqu 0x30(%rsi),%xmm3
  4924fa:	f3 0f 6f 66 40       	movdqu 0x40(%rsi),%xmm4
  4924ff:	f3 0f 6f 6e 50       	movdqu 0x50(%rsi),%xmm5
  492504:	f3 0f 6f 76 60       	movdqu 0x60(%rsi),%xmm6
  492509:	f3 0f 6f 7e 70       	movdqu 0x70(%rsi),%xmm7
  49250e:	f3 44 0f 6f 86 80 00 	movdqu 0x80(%rsi),%xmm8
  492515:	00 00 
  492517:	f3 44 0f 6f 8e 90 00 	movdqu 0x90(%rsi),%xmm9
  49251e:	00 00 
  492520:	f3 44 0f 6f 96 a0 00 	movdqu 0xa0(%rsi),%xmm10
  492527:	00 00 
  492529:	f3 44 0f 6f 9e b0 00 	movdqu 0xb0(%rsi),%xmm11
  492530:	00 00 
  492532:	f3 44 0f 6f a6 c0 00 	movdqu 0xc0(%rsi),%xmm12
  492539:	00 00 
  49253b:	f3 44 0f 6f ae d0 00 	movdqu 0xd0(%rsi),%xmm13
  492542:	00 00 
  492544:	f3 44 0f 6f b6 e0 00 	movdqu 0xe0(%rsi),%xmm14
  49254b:	00 00 
  49254d:	f3 44 0f 6f be f0 00 	movdqu 0xf0(%rsi),%xmm15
  492554:	00 00 
  492556:	f3 0f 7f 07          	movdqu %xmm0,(%rdi)
  49255a:	f3 0f 7f 4f 10       	movdqu %xmm1,0x10(%rdi)
  49255f:	f3 0f 7f 57 20       	movdqu %xmm2,0x20(%rdi)
  492564:	f3 0f 7f 5f 30       	movdqu %xmm3,0x30(%rdi)
  492569:	f3 0f 7f 67 40       	movdqu %xmm4,0x40(%rdi)
  49256e:	f3 0f 7f 6f 50       	movdqu %xmm5,0x50(%rdi)
  492573:	f3 0f 7f 77 60       	movdqu %xmm6,0x60(%rdi)
  492578:	f3 0f 7f 7f 70       	movdqu %xmm7,0x70(%rdi)
  49257d:	f3 44 0f 7f 87 80 00 	movdqu %xmm8,0x80(%rdi)
  492584:	00 00 
  492586:	f3 44 0f 7f 8f 90 00 	movdqu %xmm9,0x90(%rdi)
  49258d:	00 00 
  49258f:	f3 44 0f 7f 97 a0 00 	movdqu %xmm10,0xa0(%rdi)
  492596:	00 00 
  492598:	f3 44 0f 7f 9f b0 00 	movdqu %xmm11,0xb0(%rdi)
  49259f:	00 00 
  4925a1:	f3 44 0f 7f a7 c0 00 	movdqu %xmm12,0xc0(%rdi)
  4925a8:	00 00 
  4925aa:	f3 44 0f 7f af d0 00 	movdqu %xmm13,0xd0(%rdi)
  4925b1:	00 00 
  4925b3:	f3 44 0f 7f b7 e0 00 	movdqu %xmm14,0xe0(%rdi)
  4925ba:	00 00 
  4925bc:	f3 44 0f 7f bf f0 00 	movdqu %xmm15,0xf0(%rdi)
  4925c3:	00 00 
  4925c5:	48 81 fb 00 01 00 00 	cmp    $0x100,%rbx
  4925cc:	48 8d b6 00 01 00 00 	lea    0x100(%rsi),%rsi
  4925d3:	48 8d bf 00 01 00 00 	lea    0x100(%rdi),%rdi
  4925da:	0f 8d 00 ff ff ff    	jge    4924e0 <runtime.memmove+0x2c0>
  4925e0:	66 45 0f ef ff       	pxor   %xmm15,%xmm15
  4925e5:	e9 3f fc ff ff       	jmp    492229 <runtime.memmove+0x9>
  4925ea:	48 89 f9             	mov    %rdi,%rcx
  4925ed:	48 29 f1             	sub    %rsi,%rcx
  4925f0:	48 39 d9             	cmp    %rbx,%rcx
  4925f3:	0f 82 ac 01 00 00    	jb     4927a5 <runtime.memmove+0x585>
  4925f9:	48 81 fb 00 00 10 00 	cmp    $0x100000,%rbx
  492600:	0f 83 c3 00 00 00    	jae    4926c9 <runtime.memmove+0x4a9>
  492606:	48 8d 0c 1e          	lea    (%rsi,%rbx,1),%rcx
  49260a:	49 89 fa             	mov    %rdi,%r10
  49260d:	f3 0f 6f 69 80       	movdqu -0x80(%rcx),%xmm5
  492612:	f3 0f 6f 71 90       	movdqu -0x70(%rcx),%xmm6
  492617:	48 c7 c0 80 00 00 00 	mov    $0x80,%rax
  49261e:	48 83 e7 e0          	and    $0xffffffffffffffe0,%rdi
  492622:	48 83 c7 20          	add    $0x20,%rdi
  492626:	f3 0f 6f 79 a0       	movdqu -0x60(%rcx),%xmm7
  49262b:	f3 44 0f 6f 41 b0    	movdqu -0x50(%rcx),%xmm8
  492631:	49 89 fb             	mov    %rdi,%r11
  492634:	4d 29 d3             	sub    %r10,%r11
  492637:	f3 44 0f 6f 49 c0    	movdqu -0x40(%rcx),%xmm9
  49263d:	f3 44 0f 6f 51 d0    	movdqu -0x30(%rcx),%xmm10
  492643:	4c 29 db             	sub    %r11,%rbx
  492646:	f3 44 0f 6f 59 e0    	movdqu -0x20(%rcx),%xmm11
  49264c:	f3 44 0f 6f 61 f0    	movdqu -0x10(%rcx),%xmm12
  492652:	c5 fe 6f 26          	vmovdqu (%rsi),%ymm4
  492656:	4c 01 de             	add    %r11,%rsi
  492659:	48 29 c3             	sub    %rax,%rbx
  49265c:	c5 fe 6f 06          	vmovdqu (%rsi),%ymm0
  492660:	c5 fe 6f 4e 20       	vmovdqu 0x20(%rsi),%ymm1
  492665:	c5 fe 6f 56 40       	vmovdqu 0x40(%rsi),%ymm2
  49266a:	c5 fe 6f 5e 60       	vmovdqu 0x60(%rsi),%ymm3
  49266f:	48 01 c6             	add    %rax,%rsi
  492672:	c5 fd 7f 07          	vmovdqa %ymm0,(%rdi)
  492676:	c5 fd 7f 4f 20       	vmovdqa %ymm1,0x20(%rdi)
  49267b:	c5 fd 7f 57 40       	vmovdqa %ymm2,0x40(%rdi)
  492680:	c5 fd 7f 5f 60       	vmovdqa %ymm3,0x60(%rdi)
  492685:	48 01 c7             	add    %rax,%rdi
  492688:	48 29 c3             	sub    %rax,%rbx
  49268b:	77 cf                	ja     49265c <runtime.memmove+0x43c>
  49268d:	48 01 c3             	add    %rax,%rbx
  492690:	48 01 fb             	add    %rdi,%rbx
  492693:	c4 c1 7e 7f 22       	vmovdqu %ymm4,(%r10)
  492698:	c5 f8 77             	vzeroupper
  49269b:	f3 0f 7f 6b 80       	movdqu %xmm5,-0x80(%rbx)
  4926a0:	f3 0f 7f 73 90       	movdqu %xmm6,-0x70(%rbx)
  4926a5:	f3 0f 7f 7b a0       	movdqu %xmm7,-0x60(%rbx)
  4926aa:	f3 44 0f 7f 43 b0    	movdqu %xmm8,-0x50(%rbx)
  4926b0:	f3 44 0f 7f 4b c0    	movdqu %xmm9,-0x40(%rbx)
  4926b6:	f3 44 0f 7f 53 d0    	movdqu %xmm10,-0x30(%rbx)
  4926bc:	f3 44 0f 7f 5b e0    	movdqu %xmm11,-0x20(%rbx)
  4926c2:	f3 44 0f 7f 63 f0    	movdqu %xmm12,-0x10(%rbx)
  4926c8:	c3                   	ret
  4926c9:	48 8d 0c 1e          	lea    (%rsi,%rbx,1),%rcx
  4926cd:	f3 0f 6f 6c 1e 80    	movdqu -0x80(%rsi,%rbx,1),%xmm5
  4926d3:	f3 0f 6f 71 90       	movdqu -0x70(%rcx),%xmm6
  4926d8:	f3 0f 6f 79 a0       	movdqu -0x60(%rcx),%xmm7
  4926dd:	f3 44 0f 6f 41 b0    	movdqu -0x50(%rcx),%xmm8
  4926e3:	f3 44 0f 6f 49 c0    	movdqu -0x40(%rcx),%xmm9
  4926e9:	f3 44 0f 6f 51 d0    	movdqu -0x30(%rcx),%xmm10
  4926ef:	f3 44 0f 6f 59 e0    	movdqu -0x20(%rcx),%xmm11
  4926f5:	f3 44 0f 6f 61 f0    	movdqu -0x10(%rcx),%xmm12
  4926fb:	c5 fe 6f 26          	vmovdqu (%rsi),%ymm4
  4926ff:	49 89 f8             	mov    %rdi,%r8
  492702:	48 83 e7 e0          	and    $0xffffffffffffffe0,%rdi
  492706:	48 83 c7 20          	add    $0x20,%rdi
  49270a:	49 89 fa             	mov    %rdi,%r10
  49270d:	4d 29 c2             	sub    %r8,%r10
  492710:	4c 29 d3             	sub    %r10,%rbx
  492713:	4c 01 d6             	add    %r10,%rsi
  492716:	48 8d 0c 1f          	lea    (%rdi,%rbx,1),%rcx
  49271a:	48 81 eb 80 00 00 00 	sub    $0x80,%rbx
  492721:	0f 18 86 c0 01 00 00 	prefetchnta 0x1c0(%rsi)
  492728:	0f 18 86 80 02 00 00 	prefetchnta 0x280(%rsi)
  49272f:	c5 fe 6f 06          	vmovdqu (%rsi),%ymm0
  492733:	c5 fe 6f 4e 20       	vmovdqu 0x20(%rsi),%ymm1
  492738:	c5 fe 6f 56 40       	vmovdqu 0x40(%rsi),%ymm2
  49273d:	c5 fe 6f 5e 60       	vmovdqu 0x60(%rsi),%ymm3
  492742:	48 81 c6 80 00 00 00 	add    $0x80,%rsi
  492749:	c5 fd e7 07          	vmovntdq %ymm0,(%rdi)
  49274d:	c5 fd e7 4f 20       	vmovntdq %ymm1,0x20(%rdi)
  492752:	c5 fd e7 57 40       	vmovntdq %ymm2,0x40(%rdi)
  492757:	c5 fd e7 5f 60       	vmovntdq %ymm3,0x60(%rdi)
  49275c:	48 81 c7 80 00 00 00 	add    $0x80,%rdi
  492763:	48 81 eb 80 00 00 00 	sub    $0x80,%rbx
  49276a:	77 b5                	ja     492721 <runtime.memmove+0x501>
  49276c:	0f ae f8             	sfence
  49276f:	c4 c1 7e 7f 20       	vmovdqu %ymm4,(%r8)
  492774:	c5 f8 77             	vzeroupper
  492777:	f3 0f 7f 69 80       	movdqu %xmm5,-0x80(%rcx)
  49277c:	f3 0f 7f 71 90       	movdqu %xmm6,-0x70(%rcx)
  492781:	f3 0f 7f 79 a0       	movdqu %xmm7,-0x60(%rcx)
  492786:	f3 44 0f 7f 41 b0    	movdqu %xmm8,-0x50(%rcx)
  49278c:	f3 44 0f 7f 49 c0    	movdqu %xmm9,-0x40(%rcx)
  492792:	f3 44 0f 7f 51 d0    	movdqu %xmm10,-0x30(%rcx)
  492798:	f3 44 0f 7f 59 e0    	movdqu %xmm11,-0x20(%rcx)
  49279e:	f3 44 0f 7f 61 f0    	movdqu %xmm12,-0x10(%rcx)
  4927a4:	c3                   	ret
  4927a5:	48 89 f8             	mov    %rdi,%rax
  4927a8:	f3 0f 6f 2e          	movdqu (%rsi),%xmm5
  4927ac:	f3 0f 6f 76 10       	movdqu 0x10(%rsi),%xmm6
  4927b1:	48 01 df             	add    %rbx,%rdi
  4927b4:	f3 0f 6f 7e 20       	movdqu 0x20(%rsi),%xmm7
  4927b9:	f3 44 0f 6f 46 30    	movdqu 0x30(%rsi),%xmm8
  4927bf:	4c 8d 57 e0          	lea    -0x20(%rdi),%r10
  4927c3:	49 89 fb             	mov    %rdi,%r11
  4927c6:	f3 44 0f 6f 4e 40    	movdqu 0x40(%rsi),%xmm9
  4927cc:	f3 44 0f 6f 56 50    	movdqu 0x50(%rsi),%xmm10
  4927d2:	49 83 e3 1f          	and    $0x1f,%r11
  4927d6:	f3 44 0f 6f 5e 60    	movdqu 0x60(%rsi),%xmm11
  4927dc:	f3 44 0f 6f 66 70    	movdqu 0x70(%rsi),%xmm12
  4927e2:	4c 31 df             	xor    %r11,%rdi
  4927e5:	48 01 de             	add    %rbx,%rsi
  4927e8:	c5 fe 6f 66 e0       	vmovdqu -0x20(%rsi),%ymm4
  4927ed:	4c 29 de             	sub    %r11,%rsi
  4927f0:	4c 29 db             	sub    %r11,%rbx
  4927f3:	48 81 fb 00 00 10 00 	cmp    $0x100000,%rbx
  4927fa:	77 7b                	ja     492877 <runtime.memmove+0x657>
  4927fc:	48 81 eb 80 00 00 00 	sub    $0x80,%rbx
  492803:	c5 fe 6f 46 e0       	vmovdqu -0x20(%rsi),%ymm0
  492808:	c5 fe 6f 4e c0       	vmovdqu -0x40(%rsi),%ymm1
  49280d:	c5 fe 6f 56 a0       	vmovdqu -0x60(%rsi),%ymm2
  492812:	c5 fe 6f 5e 80       	vmovdqu -0x80(%rsi),%ymm3
  492817:	48 81 ee 80 00 00 00 	sub    $0x80,%rsi
  49281e:	c5 fd 7f 47 e0       	vmovdqa %ymm0,-0x20(%rdi)
  492823:	c5 fd 7f 4f c0       	vmovdqa %ymm1,-0x40(%rdi)
  492828:	c5 fd 7f 57 a0       	vmovdqa %ymm2,-0x60(%rdi)
  49282d:	c5 fd 7f 5f 80       	vmovdqa %ymm3,-0x80(%rdi)
  492832:	48 81 ef 80 00 00 00 	sub    $0x80,%rdi
  492839:	48 81 eb 80 00 00 00 	sub    $0x80,%rbx
  492840:	77 c1                	ja     492803 <runtime.memmove+0x5e3>
  492842:	c4 c1 7e 7f 22       	vmovdqu %ymm4,(%r10)
  492847:	c5 f8 77             	vzeroupper
  49284a:	f3 0f 7f 28          	movdqu %xmm5,(%rax)
  49284e:	f3 0f 7f 70 10       	movdqu %xmm6,0x10(%rax)
  492853:	f3 0f 7f 78 20       	movdqu %xmm7,0x20(%rax)
  492858:	f3 44 0f 7f 40 30    	movdqu %xmm8,0x30(%rax)
  49285e:	f3 44 0f 7f 48 40    	movdqu %xmm9,0x40(%rax)
  492864:	f3 44 0f 7f 50 50    	movdqu %xmm10,0x50(%rax)
  49286a:	f3 44 0f 7f 58 60    	movdqu %xmm11,0x60(%rax)
  492870:	f3 44 0f 7f 60 70    	movdqu %xmm12,0x70(%rax)
  492876:	c3                   	ret
  492877:	48 81 eb 80 00 00 00 	sub    $0x80,%rbx
  49287e:	0f 18 86 40 fe ff ff 	prefetchnta -0x1c0(%rsi)
  492885:	0f 18 86 80 fd ff ff 	prefetchnta -0x280(%rsi)
  49288c:	c5 fe 6f 46 e0       	vmovdqu -0x20(%rsi),%ymm0
  492891:	c5 fe 6f 4e c0       	vmovdqu -0x40(%rsi),%ymm1
  492896:	c5 fe 6f 56 a0       	vmovdqu -0x60(%rsi),%ymm2
  49289b:	c5 fe 6f 5e 80       	vmovdqu -0x80(%rsi),%ymm3
  4928a0:	48 81 ee 80 00 00 00 	sub    $0x80,%rsi
  4928a7:	c5 fd e7 47 e0       	vmovntdq %ymm0,-0x20(%rdi)
  4928ac:	c5 fd e7 4f c0       	vmovntdq %ymm1,-0x40(%rdi)
  4928b1:	c5 fd e7 57 a0       	vmovntdq %ymm2,-0x60(%rdi)
  4928b6:	c5 fd e7 5f 80       	vmovntdq %ymm3,-0x80(%rdi)
  4928bb:	48 81 ef 80 00 00 00 	sub    $0x80,%rdi
  4928c2:	48 81 eb 80 00 00 00 	sub    $0x80,%rbx
  4928c9:	77 b3                	ja     49287e <runtime.memmove+0x65e>
  4928cb:	0f ae f8             	sfence
  4928ce:	c4 c1 7e 7f 22       	vmovdqu %ymm4,(%r10)
  4928d3:	c5 f8 77             	vzeroupper
  4928d6:	f3 0f 7f 28          	movdqu %xmm5,(%rax)
  4928da:	f3 0f 7f 70 10       	movdqu %xmm6,0x10(%rax)
  4928df:	f3 0f 7f 78 20       	movdqu %xmm7,0x20(%rax)
  4928e4:	f3 44 0f 7f 40 30    	movdqu %xmm8,0x30(%rax)
  4928ea:	f3 44 0f 7f 48 40    	movdqu %xmm9,0x40(%rax)
  4928f0:	f3 44 0f 7f 50 50    	movdqu %xmm10,0x50(%rax)
  4928f6:	f3 44 0f 7f 58 60    	movdqu %xmm11,0x60(%rax)
  4928fc:	f3 44 0f 7f 60 70    	movdqu %xmm12,0x70(%rax)
  492902:	c3                   	ret

Disassembly of section .fini:
