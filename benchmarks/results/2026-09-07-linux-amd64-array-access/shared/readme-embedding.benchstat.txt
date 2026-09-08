goos: linux
goarch: amd64
pkg: github.com/mmcdole/lunar/benchmarks
cpu: AMD Ryzen 9 9950X3D 16-Core Processor          
                                                │  candidate  │              gopherlua               │                 golua                 │
                                                │   sec/op    │   sec/op     vs base                 │    sec/op     vs base                 │
Embedding/case=go_to_lua_scalars                  54.55n ± 2%   54.60n ± 4%         ~ (p=0.539 n=15)   129.60n ± 3%  +137.58% (p=0.000 n=15)
Embedding/case=lua_to_go_scalar_1000              52.06µ ± 1%   92.70µ ± 2%   +78.07% (p=0.000 n=15)    69.87µ ± 2%   +34.21% (p=0.000 n=15)
Embedding/case=go_string_echo_128B                79.91n ± 1%   67.59n ± 7%   -15.42% (p=0.000 n=15)   130.30n ± 1%   +63.06% (p=0.000 n=15)
Embedding/case=prebuilt_go_table_16_4_to_lua      234.4n ± 3%   510.3n ± 1%  +117.70% (p=0.000 n=15)    873.6n ± 2%  +272.70% (p=0.000 n=15)
Embedding/case=create_fill_go_table_16_4_to_lua   2.333µ ± 9%   1.280µ ± 2%   -45.14% (p=0.000 n=15)    1.483µ ± 4%   -36.43% (p=0.000 n=15)
geomean                                           658.8n        741.0n        +12.48%                   1.089µ        +65.24%

                                                │   candidate   │                gopherlua                │                 golua                 │
                                                │     B/op      │     B/op      vs base                   │     B/op      vs base                 │
Embedding/case=go_to_lua_scalars                     0.0 ± 0%         0.0 ± 0%         ~ (p=1.000 n=15) ¹     160.0 ± 0%         ? (p=0.000 n=15)
Embedding/case=lua_to_go_scalar_1000              0.00Ki ± 0%     29.17Ki ± 0%         ? (p=0.000 n=15)     31.41Ki ± 0%         ? (p=0.000 n=15)
Embedding/case=go_string_echo_128B                  0.00 ± 0%       16.00 ± 0%         ? (p=0.000 n=15)      176.00 ± 0%         ? (p=0.000 n=15)
Embedding/case=prebuilt_go_table_16_4_to_lua        0.00 ± 0%       40.00 ± 0%         ? (p=0.000 n=15)      576.00 ± 0%         ? (p=0.000 n=15)
Embedding/case=create_fill_go_table_16_4_to_lua    647.0 ± 2%      1384.0 ± 0%  +113.91% (p=0.000 n=15)      1536.0 ± 0%  +137.40% (p=0.000 n=15)
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
