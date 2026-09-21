# ข้อจำกัด

CON-001 — Runtime configuration ต้องเก็บใน `config/*.json`; ห้ามเพิ่ม configuration แบบ `.env`

CON-002 — Session database ต้องแยกจากกัน: หนึ่ง session ต้อง map ไปยังหนึ่ง database ภายใต้ `data/sessions/`

CON-003 — ห้ามเพิ่มการเก็บ raw provider request/response ที่ทำให้ session data โตโดยไม่จำเป็น ต้อง persist เฉพาะ structured state ที่จำเป็นสำหรับ continuation, replay และ accounting

CON-004 — Agent ต้องไม่ผูกกับ provider รายใดรายหนึ่งและต้องไม่ผูกกับ transport

CON-005 — Provider adapter ห้ามแก้ไข shared adapter configuration เมื่อใช้ settings เฉพาะของ session

CON-006 — Browser automation ต้องเป็น Go CDP implementation ที่อยู่ในตัว ห้ามเพิ่ม Playwright หรือ Node.js browser worker

CON-007 — Requirement files ใน repository เป็นแหล่งอ้างอิงหลักของโปรเจคนั้น Chat memory เป็นเพียง context และใช้แทน project specification ไม่ได้

CON-008 — ห้ามลดหรือเอา requirement ที่มีอยู่ออกอย่างเงียบ ๆ เพื่อให้ implementation ใหม่ทำได้ง่ายขึ้น หากมีการเปลี่ยนโดยตั้งใจต้องบันทึกเป็น specification change

CON-009 — หลีกเลี่ยงการ refactor ที่ไม่เกี่ยวข้องขณะ implement requirement change

CON-010 — ห้ามนำเส้นทาง model-call แบบ streaming กลับมาใช้; `Generate` เป็นเส้นทาง model call เพียงเส้นทางเดียว

CON-011 — File store ของ attachment ต้องอยู่ใต้ state root (`~/.local/share/ai/data/attachments/`) เท่านั้น ไม่ใช่ใน repository/working tree และไม่ใช่ session database; ต้องมีขีดจำกัดขนาดต่อไฟล์/ต่อ session พร้อม TTL cleanup; ห้ามเก็บเนื้อหาไฟล์ใน `data/sessions/` (คง CON-002, CON-003) และ path/limit ต้องกำหนดใน `config/*.json` เท่านั้น (คง CON-001)

CON-012 — โหมด Kaggle compute ต้องไม่ใช้ local SQLite/session files เป็น source of truth; durable session/job/event state ต้องอยู่ใน Cloudflare D1 ผ่าน authenticated API และไฟล์/DB ชั่วคราวบน Kaggle ต้องถือเป็น disposable compute state เท่านั้น
