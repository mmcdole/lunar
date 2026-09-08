Five paired CBOR load CPU profiles compare the same number of completed operations. The tables below use absolute sampled CPU across all five loads; per-load values divide by five. Samples occur every 10 ms. These profiled runs provide attribution, not speed qualification; the separate unprofiled comparison remains the performance result.

| Lane | Loads | Sampled CPU total | CPU per load | Sample events |
| --- | ---: | ---: | ---: | ---: |
| baseline | 5 | 8.860 s | 1772.0 ms | 886 |
| candidate | 5 | 8.960 s | 1792.0 ms | 896 |

Normalized cumulative shares divide a function’s cumulative sampled CPU by that lane’s own total. Cumulative values overlap along call stacks and must not be added. A share change can result from another part of the program changing. None of these shares is a removable cost or a predicted speedup. Inline attribution and instruction layout can move samples between adjacent source lines; a line difference does not establish that the new guard caused the workload regression.

| Pair (actual order) | Baseline sampled CPU | Candidate sampled CPU | Difference |
| --- | ---: | ---: | ---: |
| 1 (B → C) | 1770 ms | 1780 ms | +10 ms |
| 2 (C → B) | 1780 ms | 1790 ms | +10 ms |
| 3 (B → C) | 1760 ms | 1810 ms | +50 ms |
| 4 (C → B) | 1780 ms | 1810 ms | +30 ms |
| 5 (B → C) | 1770 ms | 1770 ms | +0 ms |

Table paths and surrounding operations:

| Function | B flat | C flat | B cumulative | C cumulative | B share | C share |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| `(*State).measureSemanticHeap` | 20 ms | 10 ms | 110 ms | 110 ms | 1.24% | 1.23% |
| `(*semanticHeapSummary).addStringBacking` | 10 ms | 0 ms | 90 ms | 100 ms | 1.02% | 1.12% |
| `(*tableObject).existingArrayIndex` | 0 ms | 40 ms | 0 ms | 40 ms | 0.00% | 0.45% |
| `(*tableObject).rawNormalizedSlot` | 10 ms | 0 ms | 10 ms | 0 ms | 0.11% | 0.00% |
| `(*tableObject).rawSetNormalizedSlot` | 0 ms | 0 ms | 170 ms | 280 ms | 1.92% | 3.12% |
| `(*tableObject).rawSlot` | 20 ms | 10 ms | 50 ms | 80 ms | 0.56% | 0.89% |
| `(*tableObject).rawStringKeySlot` | 0 ms | 0 ms | 200 ms | 130 ms | 2.26% | 1.45% |
| `(*tableObject).resolveNormalizedSlot` | 10 ms | 30 ms | 50 ms | 40 ms | 0.56% | 0.45% |
| `executeRawTableSet` | 0 ms | 0 ms | 230 ms | 340 ms | 2.60% | 3.79% |
| `hashNumber` | 0 ms | 0 ms | 10 ms | 10 ms | 0.11% | 0.11% |
| `normalizeTableKey` | 20 ms | 40 ms | 30 ms | 50 ms | 0.34% | 0.56% |
| `positiveIntegerIndex` | 10 ms | 0 ms | 10 ms | 0 ms | 0.11% | 0.00% |
| `runAutomaticCollection` | 0 ms | 0 ms | 170 ms | 180 ms | 1.92% | 2.01% |

Largest exclusive CPU costs across either lane:

| Function | B flat | C flat | B cumulative | C cumulative | B share | C share |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| `runInstructions` | 1150 ms | 1180 ms | 3110 ms | 3090 ms | 35.10% | 34.49% |
| `driveExecution` | 620 ms | 540 ms | 8710 ms | 8740 ms | 98.31% | 97.54% |
| `invokeNativeCall` | 540 ms | 410 ms | 8690 ms | 8690 ms | 98.08% | 96.99% |
| `(*threadObject).publishFunctionCall` | 460 ms | 530 ms | 480 ms | 550 ms | 5.42% | 6.14% |
| `(*threadObject).planFunctionCall` | 430 ms | 450 ms | 630 ms | 650 ms | 7.11% | 7.25% |
| `(*threadObject).commitTailCall` | 250 ms | 130 ms | 430 ms | 230 ms | 4.85% | 2.57% |
| `(*threadObject).fillNil` | 230 ms | 180 ms | 260 ms | 220 ms | 2.93% | 2.46% |
| `(*threadObject).replaceFunctionCall` | 120 ms | 210 ms | 830 ms | 800 ms | 9.37% | 8.93% |
| `(*threadObject).pushFunctionCall` | 200 ms | 150 ms | 850 ms | 740 ms | 9.59% | 8.26% |
| `(*threadObject).tryCompleteFixedLuaReturn` | 150 ms | 180 ms | 340 ms | 300 ms | 3.84% | 3.35% |
| `stringByte` | 50 ms | 180 ms | 160 ms | 310 ms | 1.81% | 3.46% |
| `(*threadObject).finishNativeCall` | 90 ms | 170 ms | 110 ms | 190 ms | 1.24% | 2.12% |
| `slowArithmetic` | 120 ms | 160 ms | 190 ms | 270 ms | 2.14% | 3.01% |
| `(*threadObject).finishLuaCall` | 160 ms | 140 ms | 280 ms | 270 ms | 3.16% | 3.01% |
| `(*threadObject).clearInactive` | 160 ms | 90 ms | 170 ms | 100 ms | 1.92% | 1.12% |
| `slowTableGet` | 130 ms | 150 ms | 460 ms | 440 ms | 5.19% | 4.91% |
| `stringSub` | 120 ms | 140 ms | 680 ms | 790 ms | 7.67% | 8.82% |
| `Frame.ReturnNumber` | 130 ms | 70 ms | 170 ms | 170 ms | 1.92% | 1.90% |
| `writeSlot` | 120 ms | 80 ms | 170 ms | 100 ms | 1.92% | 1.12% |
| `instruction.a` | 120 ms | 110 ms | 120 ms | 110 ms | 1.35% | 1.23% |
| `(*threadObject).completeLuaReturn` | 60 ms | 120 ms | 310 ms | 250 ms | 3.50% | 2.79% |
| `(*upvalue).read` | 120 ms | 70 ms | 120 ms | 70 ms | 1.35% | 0.78% |
| `executeRawStringTableGet` | 110 ms | 60 ms | 230 ms | 90 ms | 2.60% | 1.00% |
| `(*threadObject).planFunctionCallLayout` | 60 ms | 110 ms | 90 ms | 120 ms | 1.02% | 1.34% |
| `instruction.b` | 110 ms | 110 ms | 110 ms | 110 ms | 1.24% | 1.23% |

Detailed reports: [baseline annotated table paths](baseline-annotated.txt), [candidate annotated table paths](candidate-annotated.txt), [baseline full CPU table](baseline-top.txt), [candidate full CPU table](candidate-top.txt), and [exact weights for every function and run](summary.json).

