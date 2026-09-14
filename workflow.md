# แผนผังการทำงาน AI Harness

ใช้ไฟล์นี้เป็นแบบแปลนสำหรับแก้โค้ด แสดงเฉพาะลำดับการทำงานหลักและจุดเชื่อมสำคัญ

## การทำงานหลัก

```text
แหล่งรับข้อมูล
  ↓
HarnessLoop
  ↓
Session
  ↓
Context
  ↓
Agent
  ↓
Router
  ↓
Provider
  ↓
Response
  ↓
มี Tool หรือไม่?
 ├─ มี → Tool → Session → Context
 │                    └────→ Agent
 └─ ไม่มี → Output
```

## จุดเชื่อมกับโค้ด

```text
HarnessLoop.Run()
  → HarnessLoop.Handle()
  → ResolveSession()
  → BuildRequest()
  → Agent.RunTurnWithTrace()

Agent
  → runTurn()
  → runAttempt()
  → session.Append(user)
  → buildContextWindow()
  → RouterClient.Generate()
  → Provider.Generate()

Response
  → commitResponse()
  → ตรวจ Tool

Tool
  → Execute
  → session.Append(ToolResult)
  → buildContextWindow()
  → RouterClient.Generate()
```

## ส่งผลลัพธ์

```text
Response สุดท้าย
  ↓
Agent
  ↓
HarnessLoop
  ↓
DispatchDisplay()
  ↓
แหล่งรับข้อมูล / ผู้ใช้
```

## การลองใหม่

```text
Provider
  ↓
ตรวจว่าลองใหม่ได้หรือไม่
 ├─ ได้ → รอ → Provider
 └─ ไม่ได้ → Response / Error
```

## การจัดการข้อผิดพลาด

```text
ฟังก์ชัน
  ↓
Error
  ↓
ฟังก์ชันที่เรียก
  ↓
ส่งกลับ / บันทึก / แสดงผล
```

## การเรียกโมเดล (non-stream เท่านั้น)

```text
Agent
  ↓
Router.Generate()
  ↓
Provider.Generate()
  ↓
Response
  ↓
มี Tool หรือไม่?
```

## กฎการใช้แผนผัง

แก้โค้ดโดยยึดลำดับหลักนี้ก่อน:

```text
Input
→ Session
→ Context
→ Agent
→ Router
→ Provider
→ Response
→ Tool?
→ Context ใหม่
→ Output
```

การลองใหม่ และการจัดการข้อผิดพลาด เป็นเส้นทางแยก ไม่ต้องนำรายละเอียดมาปนกับเส้นทางหลัก
