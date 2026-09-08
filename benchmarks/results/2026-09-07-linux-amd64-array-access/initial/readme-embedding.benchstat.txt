goos: linux
goarch: amd64
pkg: github.com/mmcdole/lunar/benchmarks
cpu: AMD Ryzen 9 9950X3D 16-Core Processor          
                                                │  candidate  │              gopherlua               │                 golua                 │
                                                │   sec/op    │   sec/op     vs base                 │    sec/op     vs base                 │
Embedding/case=go_to_lua_scalars                  53.63n ± 1%   51.87n ± 2%    -3.28% (p=0.000 n=15)   125.50n ± 0%  +134.01% (p=0.000 n=15)
Embedding/case=lua_to_go_scalar_1000              51.65µ ± 1%   98.21µ ± 1%   +90.13% (p=0.000 n=15)    68.45µ ± 1%   +32.52% (p=0.000 n=15)
Embedding/case=go_string_echo_128B                76.58n ± 1%   66.80n ± 0%   -12.77% (p=0.000 n=15)   127.90n ± 1%   +67.01% (p=0.000 n=15)
Embedding/case=prebuilt_go_table_16_4_to_lua      223.5n ± 0%   514.4n ± 1%  +130.16% (p=0.000 n=15)    852.4n ± 1%  +281.39% (p=0.000 n=15)
Embedding/case=create_fill_go_table_16_4_to_lua   2.308µ ± 8%   1.280µ ± 1%   -44.54% (p=0.000 n=15)    1.470µ ± 0%   -36.31% (p=0.000 n=15)
geomean                                           642.4n        741.4n        +15.41%                   1.066µ        +65.94%

                                                │   candidate   │                gopherlua                │                 golua                 │
                                                │     B/op      │     B/op      vs base                   │     B/op      vs base                 │
Embedding/case=go_to_lua_scalars                     0.0 ± 0%         0.0 ± 0%         ~ (p=1.000 n=15) ¹     160.0 ± 0%         ? (p=0.000 n=15)
Embedding/case=lua_to_go_scalar_1000              0.00Ki ± 0%     29.17Ki ± 0%         ? (p=0.000 n=15)     31.41Ki ± 0%         ? (p=0.000 n=15)
Embedding/case=go_string_echo_128B                  0.00 ± 0%       16.00 ± 0%         ? (p=0.000 n=15)      176.00 ± 0%         ? (p=0.000 n=15)
Embedding/case=prebuilt_go_table_16_4_to_lua        0.00 ± 0%       40.00 ± 0%         ? (p=0.000 n=15)      576.00 ± 0%         ? (p=0.000 n=15)
Embedding/case=create_fill_go_table_16_4_to_lua    654.0 ± 2%      1384.0 ± 0%  +111.62% (p=0.000 n=15)      1536.0 ± 0%  +134.86% (p=0.000 n=15)
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
