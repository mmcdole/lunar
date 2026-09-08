goos: linux
goarch: amd64
pkg: github.com/mmcdole/lunar/benchmarks
cpu: AMD Ryzen 9 9950X3D 16-Core Processor          
                                                │  candidate  │              gopherlua               │                 golua                 │
                                                │   sec/op    │   sec/op     vs base                 │    sec/op     vs base                 │
Embedding/case=go_to_lua_scalars                  53.92n ± 0%   51.01n ± 1%    -5.40% (p=0.000 n=15)   123.50n ± 1%  +129.04% (p=0.000 n=15)
Embedding/case=lua_to_go_scalar_1000              51.60µ ± 0%   89.80µ ± 1%   +74.03% (p=0.000 n=15)    67.53µ ± 1%   +30.87% (p=0.000 n=15)
Embedding/case=go_string_echo_128B                77.79n ± 1%   66.06n ± 1%   -15.08% (p=0.000 n=15)   126.30n ± 0%   +62.36% (p=0.000 n=15)
Embedding/case=prebuilt_go_table_16_4_to_lua      229.8n ± 0%   519.7n ± 1%  +126.15% (p=0.000 n=15)    846.7n ± 1%  +268.45% (p=0.000 n=15)
Embedding/case=create_fill_go_table_16_4_to_lua   2.164µ ± 6%   1.250µ ± 0%   -42.24% (p=0.000 n=15)    1.453µ ± 0%   -32.86% (p=0.000 n=15)
geomean                                           640.3n        722.3n        +12.80%                   1.053µ        +64.48%

                                                │   candidate   │                gopherlua                │                 golua                 │
                                                │     B/op      │     B/op      vs base                   │     B/op      vs base                 │
Embedding/case=go_to_lua_scalars                     0.0 ± 0%         0.0 ± 0%         ~ (p=1.000 n=15) ¹     160.0 ± 0%         ? (p=0.000 n=15)
Embedding/case=lua_to_go_scalar_1000              0.00Ki ± 0%     29.17Ki ± 0%         ? (p=0.000 n=15)     31.41Ki ± 0%         ? (p=0.000 n=15)
Embedding/case=go_string_echo_128B                  0.00 ± 0%       16.00 ± 0%         ? (p=0.000 n=15)      176.00 ± 0%         ? (p=0.000 n=15)
Embedding/case=prebuilt_go_table_16_4_to_lua        0.00 ± 0%       40.00 ± 0%         ? (p=0.000 n=15)      576.00 ± 0%         ? (p=0.000 n=15)
Embedding/case=create_fill_go_table_16_4_to_lua    657.0 ± 2%      1384.0 ± 0%  +110.65% (p=0.000 n=15)      1536.0 ± 0%  +133.79% (p=0.000 n=15)
geomean                                                       ²                 ?                       ²     956.6       ?
¹ all samples are equal
² summaries must be >0 to compute geomean

                                                │   candidate   │               gopherlua                │                 golua                 │
                                                │   allocs/op   │  allocs/op   vs base                   │  allocs/op   vs base                  │
Embedding/case=go_to_lua_scalars                   0.000 ± 0%      0.000 ± 0%         ~ (p=1.000 n=15) ¹    5.000 ± 0%          ? (p=0.000 n=15)
Embedding/case=lua_to_go_scalar_1000              0.000k ± 0%     1.085k ± 0%         ? (p=0.000 n=15)     4.005k ± 0%          ? (p=0.000 n=15)
Embedding/case=go_string_echo_128B                 0.000 ± 0%      1.000 ± 0%         ? (p=0.000 n=15)      5.000 ± 0%          ? (p=0.000 n=15)
Embedding/case=prebuilt_go_table_16_4_to_lua        0.00 ± 0%       0.00 ± 0%         ~ (p=1.000 n=15) ¹    57.00 ± 0%          ? (p=0.000 n=15)
Embedding/case=create_fill_go_table_16_4_to_lua    6.000 ± 0%     37.000 ± 0%  +516.67% (p=0.000 n=15)     89.000 ± 0%  +1383.33% (p=0.000 n=15)
geomean                                                       ²                ?                       ²    55.10       ?
¹ all samples are equal
² summaries must be >0 to compute geomean
