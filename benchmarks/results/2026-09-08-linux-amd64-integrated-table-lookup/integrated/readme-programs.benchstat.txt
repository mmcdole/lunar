goos: linux
goarch: amd64
pkg: github.com/mmcdole/lunar/benchmarks
cpu: AMD Ryzen 9 9950X3D 16-Core Processor          
                               │  candidate  │               gopherlua               │                 golua                 │
                               │   sec/op    │    sec/op     vs base                 │    sec/op     vs base                 │
Programs/program=binarytrees     131.7m ± 1%    153.8m ± 1%   +16.78% (p=0.000 n=15)    159.2m ± 0%   +20.92% (p=0.000 n=15)
Programs/program=fannkuchredux   14.46m ± 0%    29.25m ± 1%  +102.23% (p=0.000 n=15)    32.59m ± 1%  +125.33% (p=0.000 n=15)
Programs/program=nbody           42.80m ± 0%   161.24m ± 1%  +276.75% (p=0.000 n=15)   170.93m ± 1%  +299.39% (p=0.000 n=15)
Programs/program=spectralnorm    42.10m ± 0%   144.71m ± 0%  +243.75% (p=0.000 n=15)   133.44m ± 3%  +216.98% (p=0.000 n=15)
geomean                          43.04m         101.2m       +135.16%                   104.3m       +142.35%

                               │  candidate   │                   gopherlua                   │                     golua                     │
                               │     B/op     │       B/op        vs base                     │       B/op        vs base                     │
Programs/program=binarytrees     65.74Mi ± 0%       72.13Mi ± 0%        +9.71% (p=0.000 n=15)       99.03Mi ± 0%       +50.64% (p=0.000 n=15)
Programs/program=fannkuchredux   1.162Ki ± 0%     384.424Ki ± 0%    +32979.83% (p=0.000 n=15)   11679.282Ki ± 0%  +1004907.14% (p=0.000 n=15)
Programs/program=nbody           2.733Ki ± 0%   45633.275Ki ± 0%  +1669370.31% (p=0.000 n=15)   72508.992Ki ± 0%  +2652604.82% (p=0.000 n=15)
Programs/program=spectralnorm    28.03Ki ± 0%    57808.46Ki ± 0%   +206150.18% (p=0.000 n=15)    77569.96Ki ± 0%   +276655.64% (p=0.000 n=15)
geomean                          49.48Ki            16.16Mi         +33334.86%                      49.61Mi        +102577.39%

                               │  candidate  │                  gopherlua                  │                     golua                     │
                               │  allocs/op  │   allocs/op     vs base                     │    allocs/op     vs base                      │
Programs/program=binarytrees     1.009M ± 0%      1.010M ± 0%        +0.08% (p=0.000 n=15)       2.534M ± 0%       +151.09% (p=0.000 n=15)
Programs/program=fannkuchredux    15.00 ± 0%     1559.00 ± 0%    +10293.33% (p=0.000 n=15)   1494687.00 ± 0%   +9964480.00% (p=0.000 n=15)
Programs/program=nbody            25.00 ± 0%   376439.00 ± 0%  +1505656.00% (p=0.000 n=15)   8080629.00 ± 0%  +32322416.00% (p=0.000 n=15)
Programs/program=spectralnorm     35.00 ± 0%   231170.00 ± 0%   +660385.71% (p=0.000 n=15)   9925272.00 ± 0%  +28357820.00% (p=0.000 n=15)
geomean                           339.2           108.2k         +31791.55%                      4.175M        +1230503.97%
