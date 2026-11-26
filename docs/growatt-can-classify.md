# growatt-can-classify: offline candump classifier

`growatt-can-classify` reads candump text from a local file or standard input and reports, per frame and per interface, which documented Growatt CAN families the frame could belong to. It is offline evidence tooling: it opens no CAN interface, sends nothing, activates no runtime profile and never reports a frame as physically qualified or admitted (`physical_qualified` and `runtime_admission` are always `false`).

## Build and run

```
go build -o growatt-can-classify ./cmd/growatt-can-classify
./growatt-can-classify -input capture.txt
./growatt-can-classify -format text < capture.txt
```

## Flags

| Flag | Default | Meaning |
| --- | --- | --- |
| `-format` | `json` | `json` or `text`. |
| `-input` | `-` | A regular local text file, or `-` for standard input. |
| `-mon-association` | empty | Comma-separated interface names with an explicit descriptive MON association. |
| `-max-bytes` | 4194304 | Maximum input bytes (upper limit is the default). |
| `-max-line` | 4096 | Maximum line bytes (upper limit is the default). |

## Exit statuses

- `0`: input classified and the result written.
- `1`: input is not a regular local file or cannot be opened, a size bound or the 16384-record bound was exceeded or the input could not be read, or the result could not be written. The message goes to standard error.
- `2`: invalid flags, positional arguments, or a format other than `json` or `text`.

## Input forms

Classic candump log lines (`can0 1805F401 [8] 12 82 ...`), compact records (`can0 1805F401#1282E59B1082620F`, with optional timestamp prefix, FD `##` and remote `R` forms) are accepted. A line that does not parse is kept with its exact bytes in `raw_hex` and a diagnostic, and is never classified.

## Scope

The classifier shares no state with the configured CAN receiver. Its results are candidates graded from the documented identifier, length and association evidence; they do not select or enable a profile.
