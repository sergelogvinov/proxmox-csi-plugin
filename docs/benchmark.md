# Benchmarks

## Server 1

Test result without optimisations.
VM pinned on cpu cores.

* Intel(R) Xeon(R) E-2386G
* Soft raid 1 - 2 x NVME SAMSUNG MZQL2960HCJR

### Host

* 4K
  * READ: bw=1591MiB/s (1669MB/s), 1591MiB/s-1591MiB/s (1669MB/s-1669MB/s), io=559GiB (601GB), run=360001-360001msec
  * WRITE: bw=531MiB/s (556MB/s), 531MiB/s-531MiB/s (556MB/s-556MB/s), io=187GiB (200GB), run=360001-360001msec
* 16K
  * READ: bw=2196MiB/s (2303MB/s), 2196MiB/s-2196MiB/s (2303MB/s-2303MB/s), io=772GiB (829GB), run=360003-360003msec
  * WRITE: bw=732MiB/s (768MB/s), 732MiB/s-732MiB/s (768MB/s-768MB/s), io=257GiB (276GB), run=360003-360003msec

### VM + pvc disk

* 4k
  * READ: bw=547MiB/s (573MB/s), 547MiB/s-547MiB/s (573MB/s-573MB/s), io=192GiB (206GB), run=360002-360002msec
  * WRITE: bw=182MiB/s (191MB/s), 182MiB/s-182MiB/s (191MB/s-191MB/s), io=64.1GiB (68.8GB), run=360002-360002msec
* 16K
  * READ: bw=1690MiB/s (1772MB/s), 1690MiB/s-1690MiB/s (1772MB/s-1772MB/s), io=594GiB (638GB), run=360002-360002msec
  * WRITE: bw=563MiB/s (591MB/s), 563MiB/s-563MiB/s (591MB/s-591MB/s), io=198GiB (213GB), run=360002-360002msec

# Tests

Prepare shell script `onedisk.sh`.

```shell
#!/bin/bash

[ $# -ne 3 ] && echo Usage $0 numjobs /dev/DEVICENAME BLOCKSIZE && exit 1

fio --name=onedisk \
    --filename=$2 \
    --filesize=10g --rw=randrw --rwmixread=75 --bs=$3 --direct=1 --overwrite=1 \
    --numjobs=$1 --iodepth=32 --time_based=1 --runtime=360 \
    --ioengine=io_uring \
    --gtod_reduce=1 --group_reporting
```

## Proxmox server 1

* Intel(R) Xeon(R) E-2386G
* Soft raid 1 - 2 x NVME SAMSUNG MZQL2960HCJR

### Host machine

```shell
# sh onedisk.sh 4 /root/file 4k

onedisk: (g=0): rw=randrw, bs=(R) 4096B-4096B, (W) 4096B-4096B, (T) 4096B-4096B, ioengine=io_uring, iodepth=32
...
fio-3.33
Starting 4 processes
onedisk: Laying out IO file (1 file / 10240MiB)
Jobs: 4 (f=4): [m(4)][100.0%][r=1617MiB/s,w=536MiB/s][r=414k,w=137k IOPS][eta 00m:00s]
onedisk: (groupid=0, jobs=4): err= 0: pid=205991: Sun Sep  3 11:21:55 2023
  read: IOPS=407k, BW=1591MiB/s (1669MB/s)(559GiB/360001msec)
   bw (  MiB/s): min= 1543, max= 1649, per=100.00%, avg=1592.12, stdev= 6.40, samples=2876
   iops        : min=395024, max=422184, avg=407582.36, stdev=1638.41, samples=2876
  write: IOPS=136k, BW=531MiB/s (556MB/s)(187GiB/360001msec); 0 zone resets
   bw (  KiB/s): min=521208, max=569720, per=100.00%, avg=543497.39, stdev=2386.29, samples=2876
   iops        : min=130302, max=142430, avg=135874.34, stdev=596.57, samples=2876
  cpu          : usr=7.66%, sys=36.29%, ctx=101284505, majf=0, minf=34
  IO depths    : 1=0.1%, 2=0.1%, 4=0.1%, 8=0.1%, 16=0.1%, 32=100.0%, >=64=0.0%
     submit    : 0=0.0%, 4=100.0%, 8=0.0%, 16=0.0%, 32=0.0%, 64=0.0%, >=64=0.0%
     complete  : 0=0.0%, 4=100.0%, 8=0.0%, 16=0.0%, 32=0.1%, 64=0.0%, >=64=0.0%
     issued rwts: total=146668960,48894104,0,0 short=0,0,0,0 dropped=0,0,0,0
     latency   : target=0, window=0, percentile=100.00%, depth=32

Run status group 0 (all jobs):
   READ: bw=1591MiB/s (1669MB/s), 1591MiB/s-1591MiB/s (1669MB/s-1669MB/s), io=559GiB (601GB), run=360001-360001msec
  WRITE: bw=531MiB/s (556MB/s), 531MiB/s-531MiB/s (556MB/s-556MB/s), io=187GiB (200GB), run=360001-360001msec

Disk stats (read/write):
    md3: ios=146652094/48890416, merge=0/0, ticks=38342068/6192620, in_queue=44534688, util=100.00%, aggrios=73334651/48895282, aggrmerge=19/1005, aggrticks=19170301/3708332, aggrin_queue=22878634, aggrutil=100.00%
  nvme0n1: ios=73783133/48895282, merge=18/1005, ticks=19427962/3533561, in_queue=22961523, util=100.00%
  nvme1n1: ios=72886169/48895282, merge=21/1005, ticks=18912640/3883104, in_queue=22795745, util=100.00%
```

```shell
# sh onedisk.sh 4 /root/test 16k

onedisk: (g=0): rw=randrw, bs=(R) 16.0KiB-16.0KiB, (W) 16.0KiB-16.0KiB, (T) 16.0KiB-16.0KiB, ioengine=io_uring, iodepth=32
...
fio-3.33
Starting 4 processes
Jobs: 4 (f=4): [m(4)][100.0%][r=2231MiB/s,w=750MiB/s][r=143k,w=48.0k IOPS][eta 00m:00s]
onedisk: (groupid=0, jobs=4): err= 0: pid=207879: Sun Sep  3 11:31:18 2023
  read: IOPS=141k, BW=2196MiB/s (2303MB/s)(772GiB/360003msec)
   bw (  MiB/s): min= 1566, max= 2395, per=100.00%, avg=2197.12, stdev=31.28, samples=2876
   iops        : min=100266, max=153328, avg=140615.75, stdev=2001.84, samples=2876
  write: IOPS=46.8k, BW=732MiB/s (768MB/s)(257GiB/360003msec); 0 zone resets
   bw (  KiB/s): min=539776, max=815808, per=100.00%, avg=749899.50, stdev=10446.40, samples=2876
   iops        : min=33736, max=50988, avg=46868.70, stdev=652.90, samples=2876
  cpu          : usr=3.34%, sys=16.53%, ctx=52397217, majf=0, minf=28
  IO depths    : 1=0.1%, 2=0.1%, 4=0.1%, 8=0.1%, 16=0.1%, 32=100.0%, >=64=0.0%
     submit    : 0=0.0%, 4=100.0%, 8=0.0%, 16=0.0%, 32=0.0%, 64=0.0%, >=64=0.0%
     complete  : 0=0.0%, 4=100.0%, 8=0.0%, 16=0.0%, 32=0.1%, 64=0.0%, >=64=0.0%
     issued rwts: total=50599778,16865750,0,0 short=0,0,0,0 dropped=0,0,0,0
     latency   : target=0, window=0, percentile=100.00%, depth=32

Run status group 0 (all jobs):
   READ: bw=2196MiB/s (2303MB/s), 2196MiB/s-2196MiB/s (2303MB/s-2303MB/s), io=772GiB (829GB), run=360003-360003msec
  WRITE: bw=732MiB/s (768MB/s), 732MiB/s-732MiB/s (768MB/s-768MB/s), io=257GiB (276GB), run=360003-360003msec

Disk stats (read/write):
    md3: ios=50618639/16873910, merge=0/0, ticks=13754332/31965600, in_queue=45719932, util=100.00%, aggrios=25316786/16878196, aggrmerge=72/1088, aggrticks=6891637/29905978, aggrin_queue=36797616, aggrutil=100.00%
  nvme0n1: ios=25075468/16878196, merge=79/1088, ticks=6945223/29939499, in_queue=36884723, util=100.00%
  nvme1n1: ios=25558104/16878196, merge=65/1088, ticks=6838052/29872458, in_queue=36710510, util=100.00%
```

## Pod with pvc

```shell
# sh onedisk.sh 4 /mnt/test 4k

onedisk: (g=0): rw=randrw, bs=(R) 4096B-4096B, (W) 4096B-4096B, (T) 4096B-4096B, ioengine=io_uring, iodepth=32
...
fio-3.34
Starting 4 processes
onedisk: Laying out IO file (1 file / 10240MiB)
Jobs: 4 (f=4): [m(4)][100.0%][r=548MiB/s,w=181MiB/s][r=140k,w=46.4k IOPS][eta 00m:00s]
onedisk: (groupid=0, jobs=4): err= 0: pid=30: Sun Sep  3 09:32:43 2023
  read: IOPS=140k, BW=547MiB/s (573MB/s)(192GiB/360002msec)
   bw (  KiB/s): min=455896, max=595600, per=100.00%, avg=559983.31, stdev=3580.80, samples=2876
   iops        : min=113974, max=148900, avg=139995.64, stdev=895.21, samples=2876
  write: IOPS=46.6k, BW=182MiB/s (191MB/s)(64.1GiB/360002msec); 0 zone resets
   bw (  KiB/s): min=150664, max=199176, per=100.00%, avg=186642.97, stdev=1312.11, samples=2876
   iops        : min=37666, max=49794, avg=46660.52, stdev=328.04, samples=2876
  cpu          : usr=4.69%, sys=10.57%, ctx=5376789, majf=0, minf=29
  IO depths    : 1=0.1%, 2=0.1%, 4=0.1%, 8=0.1%, 16=0.1%, 32=100.0%, >=64=0.0%
     submit    : 0=0.0%, 4=100.0%, 8=0.0%, 16=0.0%, 32=0.0%, 64=0.0%, >=64=0.0%
     complete  : 0=0.0%, 4=100.0%, 8=0.0%, 16=0.0%, 32=0.1%, 64=0.0%, >=64=0.0%
     issued rwts: total=50377721,16791103,0,0 short=0,0,0,0 dropped=0,0,0,0
     latency   : target=0, window=0, percentile=100.00%, depth=32

Run status group 0 (all jobs):
   READ: bw=547MiB/s (573MB/s), 547MiB/s-547MiB/s (573MB/s-573MB/s), io=192GiB (206GB), run=360002-360002msec
  WRITE: bw=182MiB/s (191MB/s), 182MiB/s-182MiB/s (191MB/s-191MB/s), io=64.1GiB (68.8GB), run=360002-360002msec

Disk stats (read/write):
  sdb: ios=50349438/16781710, merge=0/4, ticks=33038153/10156053, in_queue=43194262, util=100.00%
```

```shell
# sh onedisk.sh 4 /mnt/test 8k

onedisk: (g=0): rw=randrw, bs=(R) 8192B-8192B, (W) 8192B-8192B, (T) 8192B-8192B, ioengine=io_uring, iodepth=32
...
fio-3.34
Starting 4 processes
Jobs: 4 (f=4): [m(4)][100.0%][r=969MiB/s,w=322MiB/s][r=124k,w=41.3k IOPS][eta 00m:00s]
onedisk: (groupid=0, jobs=4): err= 0: pid=367: Sun Sep  3 09:41:12 2023
  read: IOPS=123k, BW=963MiB/s (1010MB/s)(338GiB/360002msec)
   bw (  KiB/s): min=931456, max=1049280, per=100.00%, avg=986381.34, stdev=4476.88, samples=2876
   iops        : min=116432, max=131160, avg=123297.65, stdev=559.61, samples=2876
  write: IOPS=41.1k, BW=321MiB/s (336MB/s)(113GiB/360002msec); 0 zone resets
   bw (  KiB/s): min=306448, max=353200, per=100.00%, avg=328755.29, stdev=1758.10, samples=2876
   iops        : min=38306, max=44150, avg=41094.37, stdev=219.76, samples=2876
  cpu          : usr=4.41%, sys=10.44%, ctx=5764615, majf=0, minf=36
  IO depths    : 1=0.1%, 2=0.1%, 4=0.1%, 8=0.1%, 16=0.1%, 32=100.0%, >=64=0.0%
     submit    : 0=0.0%, 4=100.0%, 8=0.0%, 16=0.0%, 32=0.0%, 64=0.0%, >=64=0.0%
     complete  : 0=0.0%, 4=100.0%, 8=0.0%, 16=0.0%, 32=0.1%, 64=0.0%, >=64=0.0%
     issued rwts: total=44367761,14787431,0,0 short=0,0,0,0 dropped=0,0,0,0
     latency   : target=0, window=0, percentile=100.00%, depth=32

Run status group 0 (all jobs):
   READ: bw=963MiB/s (1010MB/s), 963MiB/s-963MiB/s (1010MB/s-1010MB/s), io=338GiB (363GB), run=360002-360002msec
  WRITE: bw=321MiB/s (336MB/s), 321MiB/s-321MiB/s (336MB/s-336MB/s), io=113GiB (121GB), run=360002-360002msec

Disk stats (read/write):
  sdb: ios=44354662/14783278, merge=32/13, ticks=33521354/10178081, in_queue=43699507, util=100.00%
```

```shell
# sh onedisk.sh 4 /mnt/test 16k

onedisk: (g=0): rw=randrw, bs=(R) 16.0KiB-16.0KiB, (W) 16.0KiB-16.0KiB, (T) 16.0KiB-16.0KiB, ioengine=io_uring, iodepth=32
...
fio-3.34
Starting 4 processes
Jobs: 4 (f=4): [m(4)][100.0%][r=1684MiB/s,w=566MiB/s][r=108k,w=36.2k IOPS][eta 00m:00s]
onedisk: (groupid=0, jobs=4): err= 0: pid=678: Sun Sep  3 09:55:27 2023
  read: IOPS=108k, BW=1690MiB/s (1772MB/s)(594GiB/360002msec)
   bw (  MiB/s): min= 1612, max= 1786, per=100.00%, avg=1691.02, stdev= 6.01, samples=2876
   iops        : min=103202, max=114350, avg=108225.52, stdev=384.76, samples=2876
  write: IOPS=36.0k, BW=563MiB/s (591MB/s)(198GiB/360002msec); 0 zone resets
   bw (  KiB/s): min=541984, max=616832, per=100.00%, avg=577054.10, stdev=2715.09, samples=2876
   iops        : min=33874, max=38552, avg=36065.82, stdev=169.69, samples=2876
  cpu          : usr=4.33%, sys=11.20%, ctx=6788336, majf=0, minf=28
  IO depths    : 1=0.1%, 2=0.1%, 4=0.1%, 8=0.1%, 16=0.1%, 32=100.0%, >=64=0.0%
     submit    : 0=0.0%, 4=100.0%, 8=0.0%, 16=0.0%, 32=0.0%, 64=0.0%, >=64=0.0%
     complete  : 0=0.0%, 4=100.0%, 8=0.0%, 16=0.0%, 32=0.1%, 64=0.0%, >=64=0.0%
     issued rwts: total=38942752,12977752,0,0 short=0,0,0,0 dropped=0,0,0,0
     latency   : target=0, window=0, percentile=100.00%, depth=32

Run status group 0 (all jobs):
   READ: bw=1690MiB/s (1772MB/s), 1690MiB/s-1690MiB/s (1772MB/s-1772MB/s), io=594GiB (638GB), run=360002-360002msec
  WRITE: bw=563MiB/s (591MB/s), 563MiB/s-563MiB/s (591MB/s-591MB/s), io=198GiB (213GB), run=360002-360002msec

Disk stats (read/write):
  sdb: ios=38931542/12974083, merge=62/17, ticks=34184349/9899500, in_queue=44083935, util=100.00%
```

```shell
# sh onedisk.sh 4 /mnt/test 16k (iodepth=256)

onedisk: (g=0): rw=randrw, bs=(R) 16.0KiB-16.0KiB, (W) 16.0KiB-16.0KiB, (T) 16.0KiB-16.0KiB, ioengine=io_uring, iodepth=256
...
fio-3.34
Starting 4 processes
Jobs: 4 (f=4): [m(4)][100.0%][r=1628MiB/s,w=538MiB/s][r=104k,w=34.4k IOPS][eta 00m:00s]
onedisk: (groupid=0, jobs=4): err= 0: pid=1047: Sun Sep  3 10:03:14 2023
  read: IOPS=103k, BW=1612MiB/s (1690MB/s)(567GiB/360013msec)
   bw (  MiB/s): min= 1372, max= 1885, per=100.00%, avg=1613.29, stdev=18.91, samples=2876
   iops        : min=87844, max=120668, avg=103250.38, stdev=1210.14, samples=2876
  write: IOPS=34.4k, BW=537MiB/s (563MB/s)(189GiB/360013msec); 0 zone resets
   bw (  KiB/s): min=467147, max=648544, per=100.00%, avg=550515.14, stdev=6655.87, samples=2876
   iops        : min=29196, max=40534, avg=34406.92, stdev=415.99, samples=2876
  cpu          : usr=2.89%, sys=8.44%, ctx=12706018, majf=0, minf=35
  IO depths    : 1=0.1%, 2=0.1%, 4=0.1%, 8=0.1%, 16=0.1%, 32=0.1%, >=64=100.0%
     submit    : 0=0.0%, 4=100.0%, 8=0.0%, 16=0.0%, 32=0.0%, 64=0.0%, >=64=0.0%
     complete  : 0=0.0%, 4=100.0%, 8=0.0%, 16=0.0%, 32=0.0%, 64=0.0%, >=64=0.1%
     issued rwts: total=37145047,12378209,0,0 short=0,0,0,0 dropped=0,0,0,0
     latency   : target=0, window=0, percentile=100.00%, depth=256

Run status group 0 (all jobs):
   READ: bw=1612MiB/s (1690MB/s), 1612MiB/s-1612MiB/s (1690MB/s-1690MB/s), io=567GiB (609GB), run=360013-360013msec
  WRITE: bw=537MiB/s (563MB/s), 537MiB/s-537MiB/s (563MB/s-563MB/s), io=189GiB (203GB), run=360013-360013msec

Disk stats (read/write):
  sdb: ios=37133935/12374452, merge=59/16, ticks=221395271/72436641, in_queue=293832005, util=100.00%
```

```shell
# sh onedisk.sh 4 /mnt/test 16k (iodepth=4)

onedisk: (g=0): rw=randrw, bs=(R) 16.0KiB-16.0KiB, (W) 16.0KiB-16.0KiB, (T) 16.0KiB-16.0KiB, ioengine=io_uring, iodepth=4
...
fio-3.34
Starting 4 processes
Jobs: 4 (f=4): [m(4)][100.0%][r=1067MiB/s,w=360MiB/s][r=68.3k,w=23.0k IOPS][eta 00m:00s]
onedisk: (groupid=0, jobs=4): err= 0: pid=4036: Sun Sep  3 10:10:09 2023
  read: IOPS=73.1k, BW=1142MiB/s (1197MB/s)(401GiB/360004msec)
   bw (  MiB/s): min=  687, max= 1168, per=100.00%, avg=1142.55, stdev= 7.07, samples=2876
   iops        : min=44018, max=74808, avg=73122.94, stdev=452.54, samples=2876
  write: IOPS=24.4k, BW=381MiB/s (399MB/s)(134GiB/360004msec); 0 zone resets
   bw (  KiB/s): min=228199, max=413152, per=100.00%, avg=389880.72, stdev=2975.48, samples=2876
   iops        : min=14260, max=25822, avg=24367.48, stdev=186.00, samples=2876
  cpu          : usr=5.71%, sys=17.36%, ctx=22683809, majf=0, minf=33
  IO depths    : 1=0.1%, 2=0.1%, 4=100.0%, 8=0.0%, 16=0.0%, 32=0.0%, >=64=0.0%
     submit    : 0=0.0%, 4=100.0%, 8=0.0%, 16=0.0%, 32=0.0%, 64=0.0%, >=64=0.0%
     complete  : 0=0.0%, 4=100.0%, 8=0.0%, 16=0.0%, 32=0.0%, 64=0.0%, >=64=0.0%
     issued rwts: total=26309050,8767294,0,0 short=0,0,0,0 dropped=0,0,0,0
     latency   : target=0, window=0, percentile=100.00%, depth=4

Run status group 0 (all jobs):
   READ: bw=1142MiB/s (1197MB/s), 1142MiB/s-1142MiB/s (1197MB/s-1197MB/s), io=401GiB (431GB), run=360004-360004msec
  WRITE: bw=381MiB/s (399MB/s), 381MiB/s-381MiB/s (399MB/s-399MB/s), io=134GiB (144GB), run=360004-360004msec

Disk stats (read/write):
  sdb: ios=26302626/8765212, merge=41/11, ticks=4481802/765988, in_queue=5247795, util=100.00%
```