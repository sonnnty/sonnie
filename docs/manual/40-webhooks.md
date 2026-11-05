# Webhooks

SONNIE มีระบบ Generic Webhook สำหรับรับข้อความแจ้งเตือนจากระบบภายนอก (เช่น GitHub Actions, GitLab CI, Grafana, Alertmanager หรือสคริปต์อัตโนมัติ) เพื่อส่งเข้าไปยังห้อง Discord ที่กำหนด โดยผู้ส่งไม่จำเป็นต้องล็อกอิน Discord เพียงแต่ส่งคำขอ HTTP POST พร้อมโทเค็นประจำ webhook นั้นๆ

---

## Endpoint และการยืนยันตัวตน

- **URL**: `POST https://sonnie.tapb.ch/hooks/{name}` (หรือตาม `DASHBOARD_PUBLIC_URL`)
- **Headers ยืนยันตัวตน**: ใส่โทเค็นผ่าน Header อย่างใดอย่างหนึ่ง:
  - `Authorization: Bearer <TOKEN>`
  - `X-Sonnie-Token: <TOKEN>`

> [!NOTE]
> ระบบเปรียบเทียบโทเค็นด้วยอัลกอริทึม Constant-Time เพื่อความปลอดภัย หากระบุชื่อ webhook ผิดหรือใส่โทเค็นไม่ถูกต้อง ระบบจะตอบกลับด้วยรหัส **401 Unauthorized** ทั้งสองกรณี เพื่อป้องกันไม่ให้บุคคลภายนอกสุ่มค้นหาชื่อ webhook ในระบบ

---

## รูปแบบข้อมูลที่รองรับ (Payload Formats)

### 1. แบบย่อ (Simple JSON Embed)
ส่ง JSON ที่มี `title`, `text`, `color` และ `url` ระบบจะแปลงเป็น Discord Embed รูปแบบมาตรฐานสวยงาม:
```json
{
  "title": "CPU ใช้งานสูงเกินเกณฑ์",
  "text": "เซิร์ฟเวอร์ node-01 CPU พุ่งแตะ 95%",
  "color": "#ff8800",
  "url": "https://grafana.example.com/alert/123"
}
```

### 2. แบบโครงสร้าง Discord (Discord-like JSON)
ส่งข้อความและ Embed ตามโครงสร้างของ Discord Webhook มาตรฐาน:
```json
{
  "content": "การ Deploy บน Production สำเร็จแล้ว",
  "embeds": [
    {
      "title": "Release v1.4.0",
      "description": "อัปเดตระบบอ่านแชทและเพิ่มคู่มือผู้ใช้",
      "color": 5763719
    }
  ]
}
```

### 3. ข้อความธรรมดา (Plain Text)
ส่งข้อความธรรมดาตรงๆ โดยระบุ `Content-Type: text/plain`:
```text
ระบบสำรองข้อมูลประจำวันเสร็จสิ้นสมบูรณ์
```

---

## ตัวอย่างการเรียกใช้งานด้วย `curl`

### ตัวอย่างที่ 1: ส่งแบบย่อ (Simple Embed)
```bash
curl -X POST https://sonnie.tapb.ch/hooks/grafana \
  -H "Authorization: Bearer <YOUR_WEBHOOK_TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Alert: Database Latency High",
    "text": "PostgreSQL query latency > 200ms",
    "color": "#d93a3e",
    "url": "https://grafana.example.com"
  }'
```

### ตัวอย่างที่ 2: ส่งแบบโครงสร้าง Discord
```bash
curl -X POST https://sonnie.tapb.ch/hooks/ci \
  -H "X-Sonnie-Token: <YOUR_WEBHOOK_TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{
    "content": "Build succeeded for commit 1a2b3c4",
    "embeds": [
      {
        "title": "Pipeline #428 passed",
        "color": 3985274
      }
    ]
  }'
```

### ตัวอย่างที่ 3: ส่งข้อความตัวอักษรธรรมดา
```bash
curl -X POST https://sonnie.tapb.ch/hooks/ci \
  -H "Authorization: Bearer <YOUR_WEBHOOK_TOKEN>" \
  -H "Content-Type: text/plain" \
  --data "Deploy finished on staging cluster"
```

---

## ข้อจำกัดและความปลอดภัย

- **ไม่ส่งเสียงแจ้งเตือน (No Pings)**: ข้อความทั้งหมดส่งด้วยการปิดการแท็ก (`allowed_mentions: {parse: []}`) ดังนั้นการระบุ `@everyone`, `@here`, ชื่อบทบาท หรือชื่อผู้ใช้ จะแสดงเป็นตัวอักษรธรรมดาเท่านั้น จะไม่มีการแจ้งเตือนหรือรบกวนสมาชิกในห้อง
- **ขนาดข้อมูล (Max Payload)**: รับขนาดข้อมูลสูงสุด **64 KB** ต่อคำขอ (หากเกินจะตอบกลับด้วย `413 Payload Too Large`)
- **การจำกัดอัตรา (Rate Limit)**: รับได้สูงสุด **30 ครั้งต่อนาที** ต่อหนึ่ง webhook (หากเกินจะตอบกลับด้วย `429 Too Many Requests` พร้อมส่วนหัว `Retry-After`)
- **การจำกัด Embed**: รองรับสูงสุด 10 Embeds และรวมกันไม่เกิน 6,000 ตัวอักษรตามมาตรฐาน Discord

---

## วิธีที่เจ้าของบอทเพิ่ม Webhook ใหม่

สำหรับผู้ดูแลระบบหรือเจ้าของบอท การเพิ่ม webhook ใหม่ทำได้โดยกำหนดค่าตัวแปรสภาพแวดล้อม `WEBHOOKS` ในรูปแบบ:
```
WEBHOOKS=name=token@channelID,name2=token2@channelID2
```

1. **ชื่อ (Name)**: ตัวอักษรพิมพ์เล็ก ตัวเลข เครื่องหมายขีดกลาง `-` และขีดล่าง `_`
2. **โทเค็น (Token)**: ข้อความสุ่มอย่างน้อย 16 ตัวอักษร เช่น สุ่มด้วยคำสั่ง:
   ```bash
   openssl rand -hex 24
   ```
3. **Channel ID**: ไอดีห้องข้อความ Discord ปลายทาง (เปิด Developer Mode ใน Discord คลิกขวาที่ห้องแล้วเลือก *Copy Channel ID*)
4. **สิทธิ์ของบอท**: บอทต้องมีสิทธิ์ **View Channel**, **Send Messages** (และ **Embed Links** สำหรับ Embed) ในห้องเป้าหมายนั้นๆ
