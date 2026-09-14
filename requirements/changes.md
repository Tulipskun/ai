# การเปลี่ยนแปลงข้อกำหนด

ไฟล์นี้มีเจตนาให้เป็นแบบ append-only: ทุก specification change ที่ได้รับการยอมรับต้องบันทึก requirement เดิม, requirement ใหม่, เหตุผล, พื้นที่ที่ได้รับผลกระทบ และสิ่งที่ต้องใช้ตรวจสอบ

## รูปแบบการเปลี่ยนแปลง

```text
CHANGE-XXX

Date: YYYY-MM-DD
Type: add | revise | remove
Request: <คำขอของผู้ใช้>
Conflict: <หมายเลข requirement หรือ none>
Previous: <ข้อความ requirement เดิมเมื่อมีการแก้ไข/ลบ>
New: <ข้อความ requirement ใหม่>
Reason: <เหตุผลที่เปลี่ยน specification>
Impact: <architecture/modules/tests/docs ที่ได้รับผลกระทบ>
Status: proposed | accepted | rejected
```

CHANGE-001

Date: 2026-09-14
Type: add
Request: แก้ไข prompt และการแยก tool ของ main/worker ใน turn ปกติและ streaming โดยไม่ขยายพฤติกรรมของ orchestration lifecycle
Conflict: none (ทำให้ REQ-004, REQ-012 และ REQ-015 ชัดเจนขึ้น)
Previous: การแยก role และการรักษา prompt context ยังไม่ได้ระบุไว้อย่างชัดเจน; การจัดการ main prompt อาจตัด repository requirements และ context อื่นที่ไม่เกี่ยวข้องออก
New: REQ-016 จำกัด tools ของ main agent และการ execution โดยตรง; REQ-017 รักษา worker prompt/execution tools โดยไม่แทรก planner tools ในทั้งสองเส้นทาง; REQ-018 รักษา repository/custom context และจัดการ role conflict โดยไม่ลบข้อมูลด้วย heuristic
Reason: Worker ที่มี execution tools ต้องไม่ถูกสั่งให้ทำงานเป็น planner ที่ไม่มี tools และ main defaults ต้องไม่สั่ง execution โดยตรงหรือทิ้ง repository source of truth
Impact: SDK agent request composition และ planning prompt composition; SDK worker และ CLI main prompt defaults; focused SDK/CLI regressions รวมถึง real registry tool definitions ไม่มีการเปลี่ยน architecture, configuration, persistence, lifecycle หรือ transport redesign
Validation: ตรวจ prompt ของ worker และ execution continuation ทั้ง normal/streaming; delegated worker tools และ context จริง; main tool allowlist และ execution rejection ก่อน/หลัง planning; CLI defaults/context tests; `go test ./sdk ./runtime ./cmd/ai ./transport/discord -timeout 2m`; `git diff --check`
Status: accepted

CHANGE-002

Date: 2026-09-14
Type: add
Request: orchestration แบบลำดับที่เชื่อถือได้ พร้อมการยอมรับจาก main อย่างชัดเจน, retry ใน session เดิม, plan identity ที่บันทึกไว้, parent isolation, atomic reservations และ transport-independent lifecycle routing
Conflict: none (ทำให้ REQ-001, REQ-003 และ REQ-016 ชัดเจนขึ้น; ตั้งใจแทนที่การเลื่อน worker-success โดยอัตโนมัติ)
Previous: Worker loop completion เคยเลื่อน current plan โดยอัตโนมัติ; ownership/reservation ของ job และ lifecycle transport metadata ยังไม่ได้ระบุไว้อย่างชัดเจน
New: REQ-019 กำหนดให้การเลื่อนต้องผ่านการ review และ explicit verified acceptance และใช้ event-driven waiting; REQ-020 ผูก operation กับ parent/revision/step และป้องกัน job ซ้อนกัน; REQ-021 รักษา canonical input routing และรายงาน continuation errors
Reason: blocked report ที่เป็นข้อความไม่ใช่ verified success, งานเก่าต้องไม่เปลี่ยนแผนใหม่ และ mapped session ID ต้องไม่ทำให้ transport routing หาย
Impact: SDK session plan transitions, sub-agent manager/tools/prompts, Harness lifecycle continuation routing, focused SDK/runtime/CLI/Discord tests ไม่มีการเปลี่ยน transport presentation, configuration หรือ persistence redesign
Validation: acceptance gating (รวม blocked text), retry/history continuity, stale completion, concurrent overlap, cross-parent denial, completed-plan investigation, metadata/source routing และ continuation errors; `go test ./sdk ./runtime ./cmd/ai ./transport/discord -timeout 2m` และ focused race tests
Status: accepted

CHANGE-003

Date: 2026-09-14
Type: add
Request: ปรับ Discord final/progress rendering, pagination ของ Unicode/fenced-code แบบ lossless และการ flush/error cleanup ที่เชื่อถือได้โดยไม่ต้องใช้ live Discord
Conflict: none (ทำให้ REQ-001, REQ-002, REQ-009, REQ-015 และ REQ-021 ชัดเจนขึ้น; แทนที่ raw output JSON และการแสดงข้อความที่ทำให้ข้อมูลหาย)
Previous: Discord presentation, argument privacy, pagination และ flush failure behavior ยังไม่ได้ระบุไว้อย่างชัดเจน
New: REQ-022 กำหนด response ที่อ่านง่ายและ progress ที่กระชับและไม่เปิดเผยข้อมูลภายใน; REQ-023 กำหนด final/streamed pagination แบบ lossless โดยไม่ replay terminal content; REQ-024 กำหนดการเก็บ buffer, การแสดง failure, การ track page ที่ส่งสำเร็จเพื่อ retry โดยไม่ซ้ำ และ terminal cleanup
Reason: ผู้ใช้ปลายทางต้องการคำตอบที่อ่านง่ายแทน SDK envelope, execution parameters ต้องไม่รั่วผ่าน progress และ response ที่ยาว มีหลายภาษา หรือมี code ต้องไม่สูญหาย รวมถึงกรณีส่งข้อความล้มเหลว
Impact: Discord adapter/gateway, paginator ที่อยู่ใน transport และ rendering/routing tests แบบ mock ไม่มีการเปลี่ยน core orchestration, persistence, configuration หรือ transport contract redesign
Validation: round trip ของ Thai/emoji และ whitespace, fenced code ขนาดยาว, streamed page update/terminal non-duplication, การซ่อน secret arguments, send/edit transition failures และ retry, terminal cleanup, throttling/footer tests เดิม; `go test ./transport/discord -timeout 2m`; `go test ./sdk ./runtime ./cmd/ai ./transport/discord -timeout 2m`; `git diff --check`; ห้ามใช้ live Discord messages หรือ credentials
Status: accepted
