# Tactical and avionics formats: what GoFi should build in

Status: research, 2026-10-16. Nothing here is implemented. It recommends
built-ins for GoFi's Frame → Decode → Parse → stages → Encode → Sink chain, and
says which formats are better handled by Python libraries in isolated workers.
Every format claim cites a source. Where a source is secondary (a vendor page,
a third-party copy, or an open-source implementation), the text says so.

## How GoFi stands today

These points shape the recommendations, and all were checked in the source.

- Each UDP datagram goes through a fresh pipeline with its own reader
  (`listener/listener.go`, `runPackets`). Raw framing keeps one datagram as one
  frame, and other framers can split a datagram that holds several records.
- `length-prefix` reads only a big-endian u16/u32 at offset 0, then the body
  (`framing/length_prefix.go`). It cannot skip a sync or magic byte, read a
  varint, read little-endian, or count the header in the length.
- `delimited` keeps the sentinel in the frame (`framing/delimiter.go`). A
  delimiter of `</event>` therefore already yields whole CoT XML documents
  from a TCP stream.
- The `xml` parser flattens to dotted keys with string values (`event.type`,
  `event.point.lat`) (`parser/xml.go`). CoT XML is usable now, but every
  number needs `double(...)` in CEL.
- CEL exposes `fields`, `meta` and `raw` as a **string** (`filter/cel.go`).
  There is no bytes variable and there are no bit or geo helpers.
- The listen source has no multicast group join.

## 1. Ranked recommendations

Effort: S is about 1–3 days, M about 1–2 weeks, L longer. Value is for GoFi's
"unknown TCP/UDP data" users.

| # | Format | What to add | Effort | Value | Licensing / export notes |
|---|---|---|---|---|---|
| 1 | **CoT XML + TAK Protocol v1** | `cot` parser that auto-detects XML, Mesh (`0xbf ver 0xbf`) and Stream (`0xbf varint`) and emits typed `cot.*` fields. `tak-stream` framing. `cot` CEL library. `cot-xml` / `tak-mesh` / `tak-stream` encoders. CoT track sample generator. SA multicast example flow. | M | Very high | TAK `.proto` files are public in ATAK-CIV ([repo](https://github.com/deptofdefense/AndroidTacticalAssaultKit-CIV/tree/master/commoncommo/core/impl/protobuf)). MITRE CoT documents are approved for public release ([MITRE 09-4937](https://pdf4pro.com/view/cursor-on-target-message-router-user-s-guide-7a69bd.html), a third-party copy). Write the code fresh; do not copy it from cnak. |
| 2 | **Generic length-field framer** | Extend `length-prefix`: `syncBytes`, `lengthOffset`, `lengthSize` (1/2/4/varint), `endian`, `lengthIncludesHeader`, `lengthAdjust`. Covers TAK Stream, ASTERIX, IRIG 106 Ch10/11, and DIS over TCP. | S | Very high | None |
| 3 | **Declarative bit-field parser** (`format: bits`) | YAML/proto schema of fields: bit offset, width, bit order, endianness, signed, scale, enum, octal display. Plus a `bytes` CEL variable and `bits()` helpers. This is how users bring their own ICDs, including controlled ones kept out of the repo. | M | Very high | The framework is generic. Schemas for controlled standards stay with the user (§4). |
| 4 | **Geo/time CEL helpers** | `geo.inBox` (antimeridian-safe), `geo.distanceM`, `geo.inPolygon`, `geo.ecefToLla`, `isStale(ts)`, `ageSeconds(ts)`. | S | High (CoT, ADS-B, AIS, DIS) | None |
| 5 | **ARINC 429 words** | `a429` preset over #3: label (octal, with optional bit reversal), SDI, data, SSM, parity check. A per-label table for BNR/BCD scale. Sample word generator. | S–M | High | Public tutorials only. Users supply their own label dictionaries (aircraft-specific ICDs are often proprietary). |
| 6 | **IRIG 106 Ch10/11 over UDP** | Strip the UDP transfer header (Formats 1 and 3). Ch10 packet framing via #2. Expose header fields. Split 1553 F1 and ARINC-429 F0 payloads into per-message frames. | M–L | High (the common way bus data reaches Ethernet) | The RCC standard is public at irig106.org. pychapter10 is BSD-3 for the Python path. |
| 7 | **NMEA 0183 / AIS** | `nmea` parser (talker, sentence, field list, checksum check). AIS payload decode through pyais (MIT) in Python. Optional built-in fragment reassembly. | S (+M) | Medium–high | NMEA 0183 is proprietary, but its sentence layer is widely documented ([gpsd](https://gpsd.gitlab.io/gpsd/NMEA.html)). |
| 8 | **ADS-B feeds** | `csv` parser with named columns (SBS-1, port 30003). `beast` framer (0x1A escape). Mode S decode left to Python. | S | Medium | pyModeS is GPL-3.0 ([PyPI](https://pypi.org/project/pyModeS/)). Let users install it; do not bundle it. |
| 9 | **ASTERIX** | Block framing via #2 (CAT u8, LEN u16 BE including the header). Expose `asterix.cat`. Item decode in Python (CroatiaControl `asterix`) or later generated from `asterix-specs`. | S (framing) / L (decode) | Medium | CroatiaControl `asterix` is GPL ([repo](https://github.com/CroatiaControlLtd/asterix)). `asterix-specs` is BSD-3 ([repo](https://github.com/zoranbosnjak/asterix-specs)). |
| 10 | **DIS (IEEE 1278.1)** | 12-byte PDU header preset over #3 (version, exercise, PDU type, family, length). Entity State decode in Python via Open-DIS. `geo.ecefToLla`. | S / M | Medium (simulation and test ranges) | Open-DIS is BSD ([open-dis.org](https://open-dis.org/)). IEEE 1278.1 text is paid. |
| 11 | **MISB ST 0601 KLV** | Python only for now (MPEG-TS demux + klvdata, MIT). Later, perhaps a generic BER-TLV decoder. | L | Low–medium | MISB documents are public, but the host blocked automated fetches. |
| 12 | **STANAG 4607 GMTI** | Python only. Packet framing via #2 if users ask for it. | M | Low | AEDP-7 reported freely available ([archive](https://web.archive.org/web/20130223054955/http://www.nato.int/structur/AC/224/standard/4607/4607.htm)). |
| 13 | **Link 16 / VMF / JREAP-C** | **No message built-ins.** Generic only: #2, #3, Python hooks, and a documented private-schema loading path. | n/a | High demand, high risk | Controlled. See §4. |
| 14 | **MIL-STD-1553** | Covered by #6 (Ch10 F1) plus a `1553` preset over #3 for command and status words. | S once #3 and #6 exist | Medium | Public tutorials only |

## 2. Per-format notes

### 2.1 Cursor on Target and the TAK Protocol

**Wire structure**
- **CoT XML.** CoT is one `<event>` holding a `<point>` and an optional
  `<detail>`.
  - Required attributes are `version`, `uid`, `type`, `time`, `start`, `stale`
    and `how`.
  - `point` carries `lat` and `lon` (WGS 84 degrees), `hae` (metres above the
    ellipsoid), and `ce`/`le` (1-sigma circular and linear error, in metres)
    ([MITRE CoT Message Router guide, Table 2-1](https://pdf4pro.com/view/cursor-on-target-message-router-user-s-guide-7a69bd.html)).
  - MITRE's public examples use `version='2.0'` and ISO 8601 UTC times such as
    `2003-08-05T18:41:09.00Z`
    ([MITRE/NDIA 2004 slides](https://ndia.dtic.mil/wp-content/uploads/2008/USCG/Wednesday/2NiessenCursorOnTarget.pdf)).
  - Unknown `hae`/`ce`/`le` values appear as `9999999.0` in practice
    ([node-cot](https://unpkg.com/@tak-ps/node-cot@14.44.3/README.md)). The
    protobuf comments say `999999`
    ([cotevent.proto](https://raw.githubusercontent.com/deptofdefense/AndroidTacticalAssaultKit-CIV/master/commoncommo/core/impl/protobuf/cotevent.proto)).
    Treat any value of 999999 or more as unknown.
- **Type hierarchy.** `type` is a "hierarchically organized hint"
  ([MITRE](https://pdf4pro.com/view/cursor-on-target-message-router-user-s-guide-7a69bd.html)).
  - For atoms (`a-…`) the pattern is `a-{affiliation}-{dimension}-{function…}`.
    Affiliation is `f`/`h`/`n`/`u`, and dimension is `G`/`A`/`S`/`U`/`P`
    (ground, air, surface, subsurface, space)
    ([FreeTAKServer docs, secondary](https://mintlify.wiki/FreeTAKTeam/FreeTakServer/concepts/cot-messages)).
  - Non-atom trees include `b-*` (bits: alerts, chat), `t-x-*` (TAK control)
    and `u-d-*` (drawings)
    ([node-cot](https://unpkg.com/@tak-ps/node-cot@14.44.3/README.md)).
  - The spec is informal and kept by TAK Product Center consensus
    ([node-cot](https://unpkg.com/@tak-ps/node-cot@14.44.3/README.md)), with a
    standards repository at `git.tak.gov/standards/takcot`.
- **TAK Protocol v1** ([protocol.txt](https://github.com/deptofdefense/AndroidTacticalAssaultKit-CIV/blob/master/commoncommo/core/impl/protobuf/protocol.txt)):
  - **Mesh (UDP).** One message per datagram: `0xbf <varint version> 0xbf <TakMessage protobuf>`.
  - **Stream (TCP).** `0xbf <varint length> <TakMessage>`, back to back. The
    version was negotiated beforehand with CoT XML control events
    `t-x-takp-v`, `t-x-takp-q` and `t-x-takp-r`. Version 0 (XML) is mandatory,
    and both sides fall back to it.
  - `TakMessage` holds `takControl` and `cotEvent`. `CotEvent` mirrors the XML
    attributes. Times are milliseconds since the Unix epoch, and `lat`/`lon`/
    `hae`/`ce`/`le` are doubles
    ([cotevent.proto](https://raw.githubusercontent.com/deptofdefense/AndroidTacticalAssaultKit-CIV/master/commoncommo/core/impl/protobuf/cotevent.proto)).
  - `Detail` has typed `contact`, `__group`, `precisionlocation`, `status`,
    `takv` and `track`. Everything else goes in `xmlDetail`, a string. On
    conflict, the receiver keeps the `xmlDetail` data
    ([detail.proto](https://raw.githubusercontent.com/deptofdefense/AndroidTacticalAssaultKit-CIV/master/commoncommo/core/impl/protobuf/detail.proto)).
- **Ports.**
  - **Mesh SA multicast:** `239.2.3.1:6969` is PyTAK's default
    ([PyTAK configuration](https://pytak.readthedocs.io/en/latest/configuration/)).
    protocol.txt says only "a well known address and port".
  - **Server inputs:** TCP 8087 and TLS 8089 are FreeTAKServer defaults
    ([FTS config, secondary](https://mintlify.wiki/FreeTAKTeam/FreeTakServer/configuration/main-config)).
    Treat both as conventions, not standards.

**GoFi mapping**

| Step | CoT XML over TCP | CoT XML over UDP | TAK Mesh (UDP) | TAK Stream (TCP) |
|---|---|---|---|---|
| Frame | `delimited` on `</event>` (works today) | raw (one datagram) | raw | new `tak-stream` mode, or #2 with `syncBytes: bf`, `lengthSize: varint` |
| Decode | none | none | header handled by the `cot` parser | none |
| Parse | `cot` (auto-detect) | `cot` | `cot` | `cot` |
| Encode | `cot-xml` | `cot-xml` | `tak-mesh` | `tak-stream` |

**Fields for CEL.** The `cot` parser should emit typed values with the same
names for XML and protobuf input:

- **Event:** `cot.uid`, `cot.type`, `cot.how`, `cot.time`, `cot.start` and
  `cot.stale` (all three as timestamps), `cot.version`.
- **Point:** `cot.lat`, `cot.lon`, `cot.hae`, `cot.ce`, `cot.le` (all
  doubles; null when 999999 or more).
- **Common detail:** `cot.callsign`, `cot.group.name`, `cot.group.role`,
  `cot.track.speed`, `cot.track.course`, `cot.remarks`.
- **Leftovers:** `cot.detail` (the remaining detail XML as a string),
  `cot.encoding` (`xml` | `tak-mesh` | `tak-stream`), `cot.takVersion`.
- **Derived:** `cot.affiliation` and `cot.dimension`.

**CEL library.** The cnak nats-bridge has a useful function set:
`matchType`, `isFriendly`/`isHostile`/`isUnknown`, `isAircraft`/`isGround`/
`isSea`, `inBounds`, `hasGroup`, `maxClassification` and `releasableTo`
(`/home/wcrum/Coding/cnak/nats-bridge/bridge/cel_cot.go`). Port the ideas,
with these improvements:

- `cot.matchType(t, "a-.-A-*")`. cnak supports only a trailing `*`, so add a
  `.` single-position wildcard so one pattern covers every affiliation.
- `cot.affiliation(t)` and `cot.dimension(t)`, instead of six separate boolean
  functions.
- A geo box that handles the antimeridian. cnak's
  `inBounds(lat, lon, [minLat, maxLat, minLon, maxLon])` fails when minLon is
  greater than maxLon.
- `cot.isStale(fields)` to compare `stale` with the current time.
- Keep classification checks out of GoFi's core. cnak's
  `maxClassification`/`releasableTo` depend on its own marking model, so
  offer them as a documented pattern instead.

**Sample generator.** The cnak sensor-simulator has a good model
(`/home/wcrum/Coding/cnak/sensor-simulator/main.go`):

- weighted entity profiles: type, `how`, speed and altitude ranges, stale
  seconds, callsign pools;
- movement patterns: linear, circular, zigzag, random walk, waypoint, orbit;
- per-track `ce`/`le` jitter.

Rebuild it in `scripts/` or `testenv/`. It should emit XML or TAK v1 to
UDP/multicast/TCP, and add malformed cases: missing `stale`, 999999 sentinels,
duplicate `uid`, a past `stale`, and a non-UTF-8 detail.

**Pitfalls**
- **Multicast reception.** `listen` needs `multicast: {group, interface}` and
  the group join. On Kubernetes, multicast rarely crosses CNI pod networks, so
  it needs `hostNetwork` or a Multus/macvlan attachment. Say so in the docs.
- **Ambiguous first byte on TCP.** A TCP CoT stream can switch from XML to
  protobuf after negotiation. The `cot` parser should sniff `<` versus `0xbf`
  on each frame. The framer cannot switch mode mid-stream, so recommend
  configuring the port for one protocol.
- **XML safety.** Use a decoder with entity expansion off, as Go's
  `encoding/xml` is. Enforce `maxBytes`.
- **Time formats.** Fractional seconds vary (`.00Z` vs `.000Z`), and some
  senders omit them, so accept the RFC 3339 variants.
- **Type grammar.** Types are case-sensitive: affiliation is lowercase and
  dimension uppercase.

### 2.2 ARINC 429

**Wire structure**
- **Word layout.** A 32-bit word: label in bits 1–8 (written in octal), SDI in
  bits 9–10, data in bits 11–29, SSM in bits 30–31, and parity in bit 32
  (normally odd) ([Wikipedia](https://en.wikipedia.org/wiki/ARINC_429)).
- **Bit order.** The label is sent MSB first, and BNR/BCD data LSB first.
  Hardware shift registers often present the label bit-reversed: label 213
  octal (0x8B) reads as 0xD1
  ([Wikipedia](https://en.wikipedia.org/wiki/ARINC_429)).
- **Encodings.**
  - BNR is two's complement with bit 29 as the sign.
  - Resolution and scale are per label.
  - SSM meanings differ: for BNR, `11` is Normal and `00` is Failure Warning;
    for BCD, `00`/`11` mean plus/minus (N/S, E/W, and so on)
    ([Wikipedia](https://en.wikipedia.org/wiki/ARINC_429)).
- **Line rates.** 12.5 or 100 kbit/s
  ([Wikipedia](https://en.wikipedia.org/wiki/ARINC_429)).
- **On networks,** words arrive inside IRIG 106 Ch10 ARINC-429 Format 0
  packets, or in vendor UDP formats whose layouts are not public (for example,
  Alta "thin-server" packets,
  [Aviation Today](https://www.aviationtoday.com/2016/01/15/alta-data-technologies-releases-multi-protocol-avionics-ethernet-converter)).

**GoFi mapping**
- **Frame:** #2 with fixed 4-byte records, or the Ch10 splitter.
- **Parse:** the `a429` preset with `labelBitOrder: reversed|wire` and a
  `wordEndian` option.
- **Encode:** the inverse, for test generators.

**CEL fields:** `a429.label` (int) and `a429.labelOctal` ("213"), `a429.sdi`,
`a429.ssm`, `a429.parityOk`, `a429.data` (raw 19 bits), `a429.value` (scaled,
when the user table defines the label), `a429.bus` and `a429.speed` (from the
Ch10 ID word).

**Pitfalls**
- The bit-reversed label is the classic bug, so make bit order explicit.
- SSM depends on the encoding.
- Label meaning is per equipment, so do not ship a "standard" label
  dictionary. Ship only a small sample table, clearly marked as an example.

### 2.3 MIL-STD-1553B

**Wire structure**
- **Bus.** 1 Mbit/s, Manchester encoded. Each word is a 3 µs sync, 16 data
  bits and an odd parity bit ([Wikipedia](https://en.wikipedia.org/wiki/MIL-STD-1553)).
- **Command word (bit times).** RT address in 4–8, T/R in 9, subaddress/mode
  in 10–14, and word count/mode code in 15–19. A word count of 0 means 32.
  Subaddress 0 or 31 marks a mode code.
- **Status word.** It flags message error, instrumentation, service request,
  broadcast received, busy, subsystem, dynamic bus control and terminal
  ([UEI tutorial](https://www.ueidaq.com/mil-std-1553-tutorial-reference-guide)).
- **Broadcast.** RT 31 is broadcast, and terminals send no status reply
  ([Wikipedia](https://en.wikipedia.org/wiki/MIL-STD-1553)).

**GoFi mapping.** Data reaches networks almost always as Ch10 1553 Format 1.
Each message there is an 8-byte intra-packet timestamp, then a 6-byte header
(block status, gap times, length), then the command, status and data words.
Bit 13 of the block status word is the bus, A or B
([RCC 106-07 Ch10 §10.6.4.2](https://www.trmc.osd.mil/wiki/download/attachments/113019518/chapter10.pdf?api=v2)).

**CEL fields:** `m1553.rt`, `m1553.tr`, `m1553.subaddress`,
`m1553.wordCount`, `m1553.isModeCode`, `m1553.bus`, `m1553.status.*`,
`m1553.data` (list of uint16), `m1553.error`.

**Pitfalls**
- RT-to-RT transfers carry two command words.
- The 16-bit words are little-endian inside Ch10.
- Data word meaning comes from the platform ICD (user-supplied).

### 2.4 IRIG 106 Chapter 10/11 (carrier for 429 and 1553)

**Wire structure**
- **Primary header.** 24 bytes, little-endian, with fields at these offsets
  ([Ch11-22 §11.2.1.1](https://www.irig106.org/docs/106-22/chapter11.pdf);
  [irig106.org handbook](https://www.irig106.org/wiki/ch10_handbook:data_file_interpretation)):
  - 0: sync `0xEB25`
  - 2: channel ID
  - 4: u32 packet length, including header and trailer
  - 8: data length
  - 12: data type version
  - 13: sequence number
  - 14: flags
  - 15: data type
  - 16: 48-bit relative time counter at 10 MHz
  - 22: header checksum
- **Type codes.** 0x19 is 1553 F1 and 0x38 is ARINC-429 F0.
- **Size and ordering.** Packets are padded to 4 bytes. The first packet is
  TMATS (type 0x01).
- **Chapter split.** Since 106-17 the packet format is in Chapter 11, and
  Chapter 10 covers recorders and transport.
- **UDP transfer header** ([Ch10-22 §10.3.9](https://www.irig106.org/docs/106-22/chapter10.pdf)):
  - **Format 1:** 4 bytes LE, with format, type (full or segmented) and a
    24-bit sequence. One datagram may hold several full packets.
  - **Format 2:** legacy and big-endian.
  - **Format 3:** recommended for new designs. 8 bytes, with a source ID and
    an offset to the packet start, so packets can span datagrams.
- **Ports.** The standard names no UDP port. TCP 10620 (streaming) and 10610
  (command/control) are its only defaults
  ([Ch10-22](https://www.irig106.org/docs/106-22/chapter10.pdf)).

**GoFi mapping**
- **Frame:**
  - a `ch10-udp` decoder strips the transfer header (Formats 1 and 3);
  - #2 then frames packets using `syncBytes: 25 eb`, `lengthOffset: 4`,
    `lengthSize: 4`, `endian: little` and `lengthIncludesHeader: true`;
  - a `ch10` splitter turns 0x19 and 0x38 payloads into one frame per bus
    message, carrying header fields into `meta`.
- **Parse:** the `a429` or `1553` presets.

**Pitfalls**
- Format 3 segmentation needs per-source reassembly state.
- Verify the header checksum.
- Treat a TMATS packet as a configuration message, not as data.
- Mainline Wireshark has no Ch10 dissector, so debugging relies on c10-tools
  ([PyPI](https://pypi.org/project/c10-tools/)).

### 2.5 Link 16 (MIL-STD-6016), VMF (MIL-STD-6017) and JREAP (MIL-STD-3011)

**What is public**
- **Link 16.** It carries J-series messages: binary words whose meanings are
  set by MIL-STD-6016 and STANAG 5516
  ([Wikipedia](https://en.wikipedia.org/wiki/Link_16)).
  - Public summaries name these families
    ([TADIL-J](https://en.wikipedia.org/wiki/TADIL-J)):
    - J0 network management;
    - J2.x PPLI;
    - J3.x surveillance tracks;
    - J7 track management;
    - J28 free text and national use.
  - Words are described as 75 bits padded to 80
    ([TADIL-J](https://en.wikipedia.org/wiki/TADIL-J)).
  - Public sources disagree on J3 sub-label numbering, so nothing should be
    hard-coded from them.
- **VMF.** Bodies are K-series messages, and the application header is
  MIL-STD-2045-47001 ([Wikipedia](https://en.wikipedia.org/wiki/Variable_Message_Format)).
- **JREAP-C.** Appendix C of MIL-STD-3011. It carries tactical-link messages
  over IP, TCP and/or UDP, unicast or multicast
  ([JREAP, secondary](https://en.wikipedia.org/wiki/JREAP);
  [Milsoft, vendor](https://milsoft.com.tr/?p=2145)).
- **No public header layout.** We found no public description of JREAP's
  header layout.
- **Open-source references.**
  - Wireshark has a GPL `link16` dissector, reached only through SISO-STD-002
    (Link 16 over DIS)
    ([packet-link16.c](https://gitlab.com/wireshark/wireshark/-/raw/master/epan/dissectors/packet-link16.c)).
  - Public SISO-STD-002 captures are on
    [Wireshark SampleCaptures](https://wiki.wireshark.org/SampleCaptures).

**What is not public.** The J-word field tables, the VMF K-series data
elements and the JREAP header/management detail are distribution-limited:
- MIL-STD-6016C carried Distribution D and an export-control warning (seen on
  a [third-party copy](https://www.scribd.com/document/53292103/Main-Section-1));
- MIL-STD-6017 is distribution C
  ([Wikipedia](https://en.wikipedia.org/wiki/Variable_Message_Format));
- MIL-STD-3011's distribution could not be confirmed publicly.

**What GoFi does instead**
- **Framing.** #2 handles any length-delimited envelope over TCP/UDP.
- **Parsing.** The #3 bit-field parser takes a schema file the user loads at
  runtime, from a Kubernetes Secret or ConfigMap or a mounted path. It is
  never committed to the repo.
- **Python.** Hooks and parsers run in the isolated worker for operators'
  licensed or government-furnished decoders.
- **Downstream.** The CoT encoder lets decoded tracks feed TAK.
- **Demo.** An example uses DIS + SISO-STD-002 public captures to show the
  envelope path without decoding J-words.

### 2.6 Adjacent formats (brief)

- **ASTERIX**
  - **Structure.** A data block is CAT (1 octet), then LEN (2 octets, which
    count CAT and LEN), then records. Each record starts with an FSPEC bitmap
    using an FX continuation bit, and the category's UAP defines which items
    are present ([EUROCONTROL](https://www.eurocontrol.int/asterix);
    [ICAO paper](https://www.icao.int/sites/default/files/Meetings/AMC/MA/2008/NACC_WG02/naccwg02ip11.pdf)).
  - **Categories.** 000–127 standard, 128–240 special, 241–255 non-standard
    ([EUROCONTROL](https://www.eurocontrol.int/asterix)).
  - **Recommendation.** Do block framing now, and multiple blocks per UDP
    datagram are common. Leave UAP decode to Python or a later generated
    decoder.
- **ADS-B / Mode S**
  - **DF17.** 112 bits: DF 5, CA 3, ICAO 24, ME 56, PI 24. Type codes 1–4
    are identification, 9–18 airborne position, and 19 velocity
    ([mode-s.org](https://mode-s.org/1090mhz/content/ads-b/1-basics.html)).
  - **Beast.** `0x1A` plus a type byte (`'1'`/`'2'`/`'3'`), a 6-byte 12 MHz
    MLAT timestamp, a signal byte, and the 2/7/14-byte message, with `0x1A`
    doubled inside data
    ([Jetvision wiki, secondary](https://wiki.jetvision.de/wiki/Mode-S_Beast:Data_Output_Formats)).
  - **SBS-1.** CSV on TCP 30003. `MSG,3` carries position and `MSG,4`
    velocity, and clients correlate by ICAO hex
    ([FlightAware forum, secondary](https://discussions.flightaware.com/t/tool-for-decoding-beast-format-ads-b-data/17007)).
  - **Recommendation.** Build in the `csv` parser and the `beast` framer;
    leave CPR position decoding to Python.
- **NMEA 0183 / AIS**
  - **Sentences.** `$` or `!`, at most 82 bytes, with an XOR checksum written
    as `*hh` ([gpsd NMEA](https://gpsd.gitlab.io/gpsd/NMEA.html)).
  - **AIVDM.** Adds fragment count, fragment number, message ID, channel, a
    6-bit armored payload and fill bits. Multi-fragment messages share an ID
    ([gpsd AIVDM](https://gpsd.gitlab.io/gpsd/AIVDM.html)).
  - **Recommendation.** The line framer exists already. Add the `nmea` parser
    and a stateful fragment joiner keyed by source, channel and message ID.
    Leave AIS bit decode to pyais
    ([MIT](https://github.com/M0r13n/pyais)).
- **DIS**
  - **Header.** 12 octets: version (5/6/7), exercise ID, PDU type, family,
    timestamp, length ([Open-DIS javadoc](https://open-dis.sourceforge.net/javadoc/open-dis/docs/edu/nps/moves/dis7mobile/PduHeader.html)).
  - **Port.** Wireshark defaults to UDP 3000, "not IANA registered"
    ([packet-dis.c](https://gitlab.com/wireshark/wireshark/-/raw/master/epan/dissectors/packet-dis.c)).
  - **Pitfall.** Entity locations are geocentric (ECEF), so it needs
    `geo.ecefToLla`.
- **MISB ST 0601**
  - **Key.** UAS Datalink LS key `06 0E 2B 34 02 0B 01 01 0E 01 03 01 01 00 00 00`.
  - **Tag order.** Tag 2 (timestamp) comes first and tag 1 (checksum) last
    ([impleo, secondary](https://impleotv.com/content/stinspector/help/docs/tests/KLV/universal-key/)).
  - **Carriage.** Sync KLV is stream_type 0x15, and async uses private_stream_1
    ([ffmpeg-devel](https://lists.ffmpeg.org/pipermail/ffmpeg-devel/2020-May/263301.html)).
  - **Recommendation.** MPEG-TS demux is out of scope for a frame pipeline;
    use Python with klvdata ([MIT](https://github.com/paretech/klvdata)).
- **STANAG 4607**
  - **Structure.** A 32-byte packet header, then segments, each with a 5-byte
    segment header
    ([Wireshark wiretap, secondary](https://gitea.osmocom.org/osmith/wireshark/src/branch/master/wiretap/stanag4607.c)).
  - **Recommendation.** Low demand; Python only.

## 3. First milestone

Five additions, in order. Each one stands alone.

1. **Generic length-field framer (#2)**
   - Acceptance tests:
     - **TAK Stream:** frames built from the protocol.txt grammar, `0xbf`
       plus a varint length (1-byte and 2-byte varints), cross-checked against
       bytes produced by takproto
       ([MIT](https://github.com/snstac/takproto)) and stored as golden files.
     - **Ch10:** frames pychapter10's `tests/sample.c10`
       ([BSD-3](https://github.com/atac/pychapter10/tree/master/tests)) with
       sync `EB25` and LE length. The packet count matches `c10 stat` from
       c10-tools.
     - **ASTERIX:** a hand-built two-block datagram
       (CAT 048 + CAT 034) yields two frames.
     - **Bad input:** a bad sync byte or an over-long length is rejected
       under `maxBytes`.
2. **`cot` parser with typed `cot.*` fields, and the `cot-xml`/`tak-mesh`/`tak-stream` encoders**
   - Acceptance tests:
     - **XML:** MITRE's public example event (uid `H#File12#16`, type
       `a-h-G-E-W-A-L`, how `m-i`, lat 30.632015, lon −86.736893,
       [NDIA slides](https://ndia.dtic.mil/wp-content/uploads/2008/USCG/Wednesday/2NiessenCursorOnTarget.pdf))
       parses to typed fields.
     - **Protobuf:** the same event encoded as TAK Mesh and Stream with
       takproto decodes identically, and times become timestamps with
       millisecond agreement.
     - **Round trip:** XML → `tak-mesh` → `cot` is lossless for the core
       fields and for `xmlDetail`.
     - **Unknowns:** `le="9999999.0"` gives a null `cot.le`.
     - **Delimited stream:** a TCP stream of three back-to-back events, framed
       on `</event>`, gives three messages.
3. **CoT CEL library and geo/time helpers (#4)**
   - Acceptance tests:
     - **Type matching:** `cot.matchType("a-h-A-M-F", "a-.-A-*")` is true;
       `cot.affiliation("a-f-G-U-C") == "f"`.
     - **Antimeridian:** a box from lon 170 to −170 contains lon 179.5.
     - **Distance:** `geo.distanceM` agrees with a reference haversine value
       to 0.5%.
     - **Staleness:** `cot.isStale` is true for the MITRE example's 2003
       `stale`.
     - **Limits:** CEL cost limits still apply (`filter/cel_limits_test.go`).
4. **CoT sample generator and the SA multicast example flow**
   - **Multicast join.** Add `listen.multicast.group`/`interface`.
   - **Example flow.** Listen on `239.2.3.1:6969`, then `cot`, then a filter
     on `cot.affiliation(fields["cot.type"]) == "h"`, then NATS and a TAK
     Stream TCP sink.
   - **Generator.** Ported ideas from cnak: weighted profiles and movement
     patterns.
   - Acceptance tests:
     - In `testenv`, the generator's hostile tracks reach the sink and others
       are dropped, with `droppedBy` metrics.
     - The Kubernetes doc states the hostNetwork/Multus requirement.
5. **Bit-field parser (#3) with `a429` and `1553` presets, and a `bytes` CEL variable**
   - Acceptance tests:
     - **Label order:** label 213 octal reads correctly from both the wire
       order and the reversed `0xD1` form
       ([Wikipedia example](https://en.wikipedia.org/wiki/ARINC_429)).
     - **SSM:** decoding matches the BNR/BCD table.
     - **Parity:** a flipped bit sets `parityOk=false`.
     - **Values:** BNR values cross-check against `arinc429`
       ([MIT, PyPI](https://pypi.org/project/arinc429/)) on generated words.
     - **1553 command word:** RT 5, T, SA 1, WC 0 decodes to word count 32.

Follow-on milestone: the Ch10 UDP decoder and splitter, and the NMEA parser.
- **Ch10 tests:** pychapter10 samples, cross-checked with `c10 dump`.
- **NMEA/AIS tests:** gpsd's single sentence
  `!AIVDM,1,1,,B,177KQJ5000G?tO`K>RA1wUbN0TKH,0*5C` and its two-fragment
  `!AIVDM,2,1,3,B,…` pair ([gpsd AIVDM](https://gpsd.gitlab.io/gpsd/AIVDM.html)).
- **ADS-B test:** the DF17 example `8D4840D6202CC371C32CE0576098`, which is
  ICAO `4840D6`, TC 4, callsign `KLM1023`
  ([mode-s.org](https://mode-s.org/1090mhz/content/ads-b/1-basics.html)).

## 4. Export-control and safety notes

**Export control**
- **Keep controlled tables out of the repo.** Do not commit MIL-STD-6016,
  6017 or 3011 field tables, message catalogs, or derived test vectors, even
  if a copy appears online. Third-party copies of MIL-STD-6016 show
  Distribution D and an Arms Export Control Act warning
  ([example](https://www.scribd.com/document/53292103/Main-Section-1)).
  MIL-STD-6017 is distribution C
  ([Wikipedia](https://en.wikipedia.org/wiki/Variable_Message_Format)).
- **What may be implemented.**
  - Generic mechanisms only: length-field framing, bit-field schemas, Python
    hooks.
  - Public format families only: CoT, TAK v1, ARINC 429 word structure, 1553
    word structure, IRIG 106, ASTERIX, NMEA/AIS, DIS, ADS-B.
- **Schemas stay out of images and the UI.** User-supplied schemas load at
  runtime from Secrets or mounts. They are not baked into images, not shown in
  public test fixtures, and not sent to the UI's shared library catalogue.
- **Contributions.** A contribution that names a J-series or K-series field
  layout should be refused, and the reason recorded in `CONTRIBUTING`.
- **Status of this document.** It is not an export determination. USML
  Category XI covers military C3I electronics, and some items moved to the EAR
  600 series
  ([Federal Register 2014](https://www.govinfo.gov/content/pkg/FR-2014-07-01/html/2014-14681.htm)).
  Deployers handling real Link 16 or VMF data need their own program's
  guidance.

**Licensing**
- GPL libraries (pyModeS, CroatiaControl `asterix`, Wireshark dissectors) are
  fine for a user to install in a Python worker. Do not vendor or bundle them
  in GoFi images.
- BSD/MIT libraries are safe to reference: pychapter10, c10-tools, takproto,
  pyais, klvdata, Open-DIS, asterix-specs.

**Safety and handling**
- **Not flight- or safety-certified.** GoFi is a best-effort,
  at-most-once pipeline (README). It must not be presented as a path for
  flight-critical avionics data or for weapons or targeting decisions. Bus
  taps should be monitor-only.
- **Encoders can transmit.** TAK multicast and TCP encoders can inject tracks
  into live common operational pictures. Gate them on explicit sink config,
  default the TTL to 1, and label simulated output with a distinct `uid`
  prefix and `how="h-e"` or a `<__simulated/>`-style detail.
- **Data stays at its marking.** Live inspection, replay, dead letters and
  Loki keep payloads. Operators handling CUI or classified feeds must run
  GoFi inside an accredited enclave. GoFi does not enforce classification
  markings, and the cnak-style `releasableTo` should stay a user policy.
- **Input hardening.** Enforce `maxBytes` for every framer, bound varint
  length at 5 bytes, verify checksums (Ch10 header, NMEA XOR, ARINC parity),
  and keep XML entity expansion off.
