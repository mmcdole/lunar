goos: linux
goarch: amd64
pkg: github.com/mmcdole/lunar/benchmarks
cpu: AMD Ryzen 9 9950X3D 16-Core Processor          
                               │  candidate  │               gopherlua               │                 golua                 │
                               │   sec/op    │    sec/op     vs base                 │    sec/op     vs base                 │
Programs/program=binarytrees     136.6m ± 5%    156.2m ± 1%   +14.39% (p=0.000 n=15)    163.1m ± 5%   +19.45% (p=0.000 n=15)
Programs/program=fannkuchredux   14.62m ± 5%    29.76m ± 0%  +103.52% (p=0.000 n=15)    33.19m ± 2%  +127.00% (p=0.000 n=15)
Programs/program=nbody           44.38m ± 1%   165.31m ± 1%  +272.50% (p=0.000 n=15)   176.16m ± 3%  +296.95% (p=0.000 n=15)
Programs/program=spectralnorm    43.04m ± 1%   144.41m ± 1%  +235.50% (p=0.000 n=15)   135.00m ± 5%  +213.63% (p=0.000 n=15)
geomean                          44.19m         102.6m       +132.25%                   106.5m       +141.04%

                               │  candidate   │                   gopherlua                   │                     golua                     │
                               │     B/op     │       B/op        vs base                     │       B/op        vs base                     │
Programs/program=binarytrees     65.76Mi ± 0%       72.13Mi ± 0%        +9.69% (p=0.000 n=15)       99.03Mi ± 0%       +50.61% (p=0.000 n=15)
Programs/program=fannkuchredux   1.162Ki ± 4%     384.424Ki ± 0%    +32979.83% (p=0.000 n=15)   11679.282Ki ± 0%  +1004907.14% (p=0.000 n=15)
Programs/program=nbody           2.733Ki ± 0%   45633.287Ki ± 0%  +1669370.74% (p=0.000 n=15)   72509.002Ki ± 0%  +2652605.18% (p=0.000 n=15)
Programs/program=spectralnorm    28.03Ki ± 0%    57808.46Ki ± 0%   +206150.18% (p=0.000 n=15)    77569.97Ki ± 0%   +276655.68% (p=0.000 n=15)
geomean                          49.48Ki            16.16Mi         +33332.87%                      49.61Mi        +102571.25%

                               │  candidate  │                  gopherlua                  │                     golua                     │
                               │  allocs/op  │   allocs/op     vs base                     │    allocs/op     vs base                      │
Programs/program=binarytrees     1.009M ± 0%      1.010M ± 0%        +0.08% (p=0.000 n=15)       2.534M ± 0%       +151.09% (p=0.000 n=15)
Programs/program=fannkuchredux    15.00 ± 0%     1559.00 ± 0%    +10293.33% (p=0.000 n=15)   1494687.00 ± 0%   +9964480.00% (p=0.000 n=15)
Programs/program=nbody            25.00 ± 0%   376439.00 ± 0%  +1505656.00% (p=0.000 n=15)   8080629.00 ± 0%  +32322416.00% (p=0.000 n=15)
Programs/program=spectralnorm     35.00 ± 0%   231170.00 ± 0%   +660385.71% (p=0.000 n=15)   9925272.00 ± 0%  +28357820.00% (p=0.000 n=15)
geomean                           339.2           108.2k         +31791.55%                      4.175M        +1230503.97%
