goos: linux
goarch: amd64
pkg: github.com/mmcdole/lunar/benchmarks
cpu: AMD Ryzen 9 9950X3D 16-Core Processor          
                               │  candidate  │               gopherlua               │                 golua                 │
                               │   sec/op    │    sec/op     vs base                 │    sec/op     vs base                 │
Programs/program=binarytrees     134.4m ± 1%    155.4m ± 1%   +15.62% (p=0.000 n=15)    161.5m ± 0%   +20.19% (p=0.000 n=15)
Programs/program=fannkuchredux   14.01m ± 0%    29.63m ± 0%  +111.57% (p=0.000 n=15)    33.08m ± 1%  +136.16% (p=0.000 n=15)
Programs/program=nbody           44.44m ± 1%   165.17m ± 0%  +271.68% (p=0.000 n=15)   175.61m ± 1%  +295.19% (p=0.000 n=15)
Programs/program=spectralnorm    42.31m ± 1%   146.18m ± 0%  +245.52% (p=0.000 n=15)   133.39m ± 4%  +215.27% (p=0.000 n=15)
geomean                          43.37m         102.7m       +136.75%                   105.8m       +143.86%

                               │  candidate   │                   gopherlua                   │                     golua                     │
                               │     B/op     │       B/op        vs base                     │       B/op        vs base                     │
Programs/program=binarytrees     65.80Mi ± 0%       72.13Mi ± 0%        +9.62% (p=0.000 n=15)       99.03Mi ± 0%       +50.51% (p=0.000 n=15)
Programs/program=fannkuchredux   1.152Ki ± 0%     384.424Ki ± 0%    +33260.17% (p=0.000 n=15)   11679.282Ki ± 0%  +1013424.15% (p=0.000 n=15)
Programs/program=nbody           2.733Ki ± 0%   45633.283Ki ± 0%  +1669370.60% (p=0.000 n=15)   72509.008Ki ± 0%  +2652605.39% (p=0.000 n=15)
Programs/program=spectralnorm    28.03Ki ± 0%    57808.46Ki ± 0%   +206150.18% (p=0.000 n=15)    77569.96Ki ± 0%   +276655.67% (p=0.000 n=15)
geomean                          49.39Ki            16.16Mi         +33398.35%                      49.61Mi        +102772.36%

                               │  candidate  │                  gopherlua                  │                     golua                     │
                               │  allocs/op  │   allocs/op     vs base                     │    allocs/op     vs base                      │
Programs/program=binarytrees     1.009M ± 0%      1.010M ± 0%        +0.08% (p=0.000 n=15)       2.534M ± 0%       +151.09% (p=0.000 n=15)
Programs/program=fannkuchredux    15.00 ± 0%     1559.00 ± 0%    +10293.33% (p=0.000 n=15)   1494687.00 ± 0%   +9964480.00% (p=0.000 n=15)
Programs/program=nbody            25.00 ± 0%   376439.00 ± 0%  +1505656.00% (p=0.000 n=15)   8080629.00 ± 0%  +32322416.00% (p=0.000 n=15)
Programs/program=spectralnorm     35.00 ± 0%   231170.00 ± 0%   +660385.71% (p=0.000 n=15)   9925272.00 ± 0%  +28357820.00% (p=0.000 n=15)
geomean                           339.2           108.2k         +31791.55%                      4.175M        +1230503.97%
