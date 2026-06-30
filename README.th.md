# Bard

บอทเพลง Discord แบบ Multi-Instance เขียนด้วย Go โดยมี **Master** บอทสำหรับรับคำสั่ง และ **Child** บอทหลายตัวสำหรับเชื่อมต่อ Voice และเล่นเพลง — แต่ละ Voice Channel ในเซิร์ฟเวอร์สามารถมี Child ของตัวเองเล่นพร้อมกันได้

---

## สถาปัตยกรรม

```
ผู้ใช้ Discord
     │  ส่งคำสั่งผ่านข้อความ
     ▼
  Master Bot  ──────────────────────────────────────────┐
  (รับคำสั่งอย่างเดียว                                  │ HTTP
   ไม่เข้า Voice)                                       │
                                    ┌───────────────────┤
                                    │                   │
                               Child Bot 0         Child Bot 1
                             (Voice + Audio)      (Voice + Audio)
                                    │                   │
                               yt-dlp + ffmpeg     yt-dlp + ffmpeg
                                    │                   │
                              Voice Channel A     Voice Channel B
```

- **Master** รับคำสั่งข้อความจาก Discord แล้วส่งต่อไปยัง Child ที่ถูกต้องผ่าน HTTP
- **Child** เข้าร่วม Voice Channel, ดาวน์โหลดเสียงด้วย yt-dlp, แปลงไฟล์ด้วย ffmpeg แล้วสตรีมไปยัง Discord
- แต่ละ Voice Channel ที่ Active จะถูก assign ให้ Child หนึ่งตัว
- Child ถูกเลือกโดยการตรวจสอบ Health — ใช้เฉพาะ Child ที่กำลังรันอยู่

---

## ความต้องการของระบบ

- Go 1.21 ขึ้นไป
- [yt-dlp](https://github.com/yt-dlp/yt-dlp)
- [ffmpeg](https://ffmpeg.org/) (รองรับ `libopus`)

```bash
# macOS
brew install yt-dlp ffmpeg

# Ubuntu / Debian
sudo apt install ffmpeg
pip install yt-dlp
```

---

## การตั้งค่า

แก้ไข `config.json`:

```json
{
  "configs": {
    "master": {
      "token": "โทเค็นบอท Master"
    },
    "child": {
      "nodes": [
        { "token": "โทเค็นบอท Child 0", "address": "localhost:8081" },
        { "token": "โทเค็นบอท Child 1", "address": "localhost:8082" }
      ]
    }
  }
}
```

- **master.token** — โทเค็นของบอท Master (รับคำสั่งข้อความเท่านั้น)
- **child.nodes** — รายการโทเค็นของบอท Child และ address สำหรับ HTTP server
- สามารถเพิ่ม Child ได้เท่าที่ต้องการ แต่ละตัวรองรับ 1 Voice Channel ในเวลาเดียวกัน

### สร้างโทเค็นบอท

1. ไปที่ [Discord Developer Portal](https://discord.com/developers/applications)
2. สร้าง Application ใหม่สำหรับแต่ละบอท (Master 1 ตัว + Child ตามจำนวนที่ต้องการ)
3. ใน **Bot** ให้เปิดใช้งาน:
   - **Message Content Intent** (เฉพาะ Master)
4. คัดลอกโทเค็นของแต่ละบอท

### สิทธิ์ของบอท

เชิญบอทแต่ละตัวด้วยสิทธิ์เหล่านี้:
- Master: `Send Messages`, `Read Message History`
- Child: `Connect`, `Speak`

---

## Docker

รันทุกอย่างด้วย Docker Compose:

```bash
# 1. ใส่ Token ของคุณใน config.docker.json
# แก้ไขค่า token ทั้งหมดใน config.docker.json ก่อน

# 2. Build Image และเริ่มต้นทุก Service
docker compose up --build
```

**`config.docker.json`** ใช้ชื่อ Service ของ Docker เป็น Address (`child0:8081`, `child1:8082`) แทน `localhost` — แก้ไขเฉพาะค่า `token` ก่อนรัน ค่า `address` ถูกต้องสำหรับ Docker แล้ว

หากต้องการเพิ่ม Child ให้ copy service ใน `docker-compose.yml` (เช่น `child2`) และเพิ่ม node ใน `config.docker.json`

---

## การรันแบบ Local

แต่ละ Instance รันเป็น Process แยกกัน:

```bash
# Terminal 1 — Master
go run main.go master

# Terminal 2 — Child 0
go run main.go child 0

# Terminal 3 — Child 1 (ไม่บังคับ)
go run main.go child 1
```

หรือ Build ก่อนแล้วค่อยรัน:

```bash
go build -o bard .

./bard master
./bard child 0
./bard child 1
```

---

## คำสั่ง

คำสั่งทั้งหมดส่งไปที่บอท **Master** ในช่องข้อความใดก็ได้

| คำสั่ง | คำอธิบาย |
|---|---|
| `!play <ชื่อเพลงหรือ URL>` | ค้นหาเพลงจาก YouTube แล้วเล่น หรือเพิ่มเข้า Queue |
| `!skip` | ข้ามเพลงปัจจุบัน |
| `!stop` | หยุดเล่นและล้าง Queue |
| `!pause` | หยุดเพลงชั่วคราว |
| `!resume` | เล่นเพลงต่อ |
| `!queue` | ดู Queue ปัจจุบัน |
| `!leave` | บอทออกจาก Voice Channel |
| `!ping` | ตรวจสอบว่าบอทออนไลน์อยู่ |

ต้องอยู่ใน Voice Channel ก่อนใช้คำสั่งทุกอย่าง บอทจะเข้า Voice Channel ของคุณอัตโนมัติเมื่อใช้คำสั่ง `!play`

---

## Multi-Voice Channel

หลาย Voice Channel ในเซิร์ฟเวอร์เดียวกันสามารถเล่นเพลงพร้อมกันได้ โดยแต่ละช่องใช้ Child คนละตัว:

```
VC-1  →  Child 0  →  เล่นเพลง A
VC-2  →  Child 1  →  เล่นเพลง B
```

- เมื่อคุณ `!play` Master จะตรวจสอบว่าคุณอยู่ใน Voice Channel ไหน
- ถ้า Channel นั้นมี Child ที่ assigned อยู่แล้ว คำสั่งจะไปที่ Child ตัวนั้น
- ถ้ายังไม่มี Master จะเลือก Child ที่ว่างและ Healthy ตัวแรก
- เมื่อ `!stop` หรือ `!leave` Child ตัวนั้นจะถูกปล่อยกลับสู่ Pool

จำนวน Voice Channel ที่เล่นพร้อมกันได้ขึ้นอยู่กับจำนวน Child ที่ตั้งค่าและกำลังรันอยู่

---

## โครงสร้างโปรเจกต์

```
Bard/
├── main.go                  — จุดเริ่มต้น, แยก mode master/child
├── config/
│   └── config.go            — โหลด config.json
├── master/
│   └── dispatcher.go        — จัดการคำสั่ง + routing ไปยัง Child
├── child/
│   └── server.go            — HTTP server + Voice + Player
├── audio/
│   ├── player.go            — Queue, เล่นเพลง, skip/stop/pause
│   └── ogg.go               — อ่าน ogg bitstream (แยก opus frames)
└── providers/
    └── ytdlp/
        └── ytdlp.go         — wrapper ของ yt-dlp (ค้นหา + ดึง URL)
```
