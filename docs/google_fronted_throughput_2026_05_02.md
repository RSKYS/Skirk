# Google-Fronted Throughput 2026-05-02

Route:

```text
socks5h://127.0.0.1:1080
```

Skirk route mode:

```text
google_front_pinned
```

Network-visible shape:

```text
TLS URL/SNI host: www.google.com
Pinned TCP IP:    216.239.38.120
HTTP Host:        real Google API host, such as www.googleapis.com or sheets.googleapis.com
```

Benchmark command shape:

```sh
./bin/skirk bench \
  --config <fronted-config> \
  --temp-workspace \
  --title skirk-bench-fronted-20260502001049 \
  --sizes 65536,262144 \
  --chunk-sizes 65536,262144
```

## Results

| Payload | Chunk | Chunks | Send | Receive | Cleanup | Send Mbps | Receive Mbps | Round Trip Mbps |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 64 KiB | 64 KiB | 1 | 7.817s | 4.362s | 2.525s | 0.067 | 0.120 | 0.043 |
| 64 KiB | 256 KiB | 1 | 5.325s | 4.794s | 2.823s | 0.098 | 0.109 | 0.052 |
| 256 KiB | 64 KiB | 4 | 22.848s | 13.019s | 11.489s | 0.092 | 0.161 | 0.058 |
| 256 KiB | 256 KiB | 1 | 18.434s | 4.221s | 2.414s | 0.114 | 0.497 | 0.093 |

Best observed case:

```text
256 KiB payload, 256 KiB chunk:
send       0.114 Mbps
receive    0.497 Mbps
round trip 0.093 Mbps
```

Cleanup:

- Temporary spreadsheet was deleted.
- Drive queries found no leftover chunk objects for benchmark sessions.

## Interpretation

The Google-fronted mode works, but throughput from this current restricted path is low. The bottleneck is not encryption or local CPU; it is repeated Google API round trips over the slow SOCKS path plus the current sequential scheduler.

The result also confirms that larger chunks help. The 256 KiB single-chunk receive path reached roughly 0.497 Mbps, while smaller payloads are dominated by fixed request latency.

## Next Throughput Work

To improve this mode:

- use larger bulk chunks, likely 512 KiB to 2 MiB;
- batch Sheets control rows;
- parallelize Drive uploads/downloads with a small worker pool;
- defer cleanup into a background compactor;
- keep small chunks only for interactive streams.

## Why This Matters

This benchmark is the realistic number for the confirmed restricted-network Google-fronted path today. It is slower than normal internet, but it proves the best currently confirmed access mode and gives concrete scheduler targets.
