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

CHANGE-004

Date: 2026-09-14
Type: revise
Request: รองรับไฟล์แนบจาก Discord แบบ end-to-end (รับไฟล์เข้า worker, worker อ่านไฟล์แล้วสรุปกลับ, ส่งไฟล์ออกแบบกลับ channel เดิม) โดย Main Agent ห้ามรับเนื้อหาไฟล์
Conflict: REQ-016 และ REQ-019 (REQ-019 เดิมบังคับให้ Main Agent อ่านผลลัพธ์/ประวัติสุดท้ายของ worker แบบดิบ ซึ่งขัดกับข้อห้ามใหม่ที่ Main Agent ห้ามรับเนื้อหาไฟล์หรือ byte stream ผ่าน tool result ของ orchestration); พื้นที่เกี่ยวเนื่อง REQ-021, REQ-022, CON-001, CON-002, CON-003, CON-004, CON-009
Previous: REQ-016 เมื่อเปิด planning, Main Agent จะได้รับเฉพาะ planning tools และ tools สำหรับ orchestration ของ sub-agent และต้องปฏิเสธ execution call โดยตรง รวมถึงหลังจากบันทึกแผนแล้วด้วย ค่าเริ่มต้นของคำสั่ง Main Agent ต้องมอบหมายการตรวจสอบโปรเจคและการดำเนินงาน แทนการสั่งให้ Main Agent ใช้ worker tools โดยตรง || REQ-019 เมื่อ worker loop จบลง ต้องสร้างเพียงผลลัพธ์เพื่อให้ Main Agent ตรวจสอบ และห้ามรับหรือเลื่อนแผนต่อโดยอัตโนมัติ Main Agent ต้องอ่านผลลัพธ์/ประวัติสุดท้าย ตรวจสอบความสำเร็จ และยอมรับ step ที่บันทึกไว้อย่างชัดเจนผ่าน orchestration ผลลัพธ์ที่ล้มเหลว หยุด หรือยังไม่สมบูรณ์ต้องสามารถ retry ต่อใน worker session เดิมได้ Planner guidance ต้องรอ lifecycle completion event แทนการ polling ซ้ำ ๆ และยังต้องมี explicit status request ให้ใช้ได้
New: REQ-016 เมื่อเปิด planning, Main Agent จะได้รับเฉพาะ planning tools, tools สำหรับ orchestration ของ sub-agent และ tool สำหรับส่งต่อ opaque file reference (ชื่อ/ชนิด/ขนาด/relative path ภายใน file store) เท่านั้น ไม่ใช่เนื้อหาไฟล์ และต้องปฏิเสธ execution call โดยตรง รวมถึงหลังจากบันทึกแผนแล้วด้วย Main Agent ห้ามได้รับ byte stream, base64, MIME data หรือเนื้อหาภายในไฟล์ผ่านช่องทางใด ๆ รวมถึงผ่าน tool result ของ orchestration ด้วย file reference ต้องถูก resolve โดย worker agent หรือ transport module เท่านั้น ค่าเริ่มต้นของคำสั่ง Main Agent ต้องมอบหมายการตรวจสอบโปรเจคและการดำเนินงาน แทนการสั่งให้ Main Agent ใช้ worker tools โดยตรง || REQ-019 เมื่อ worker loop จบลง ต้องสร้างเพียงผลลัพธ์เพื่อให้ Main Agent ตรวจสอบ และห้ามรับหรือเลื่อนแผนต่อโดยอัตโนมัติ Main Agent ต้องอ่านข้อความสรุปและหลักฐานการตรวจสอบที่ orchestration จัดให้ ตรวจสอบความสำเร็จ และยอมรับ step ที่บันทึกไว้อย่างชัดเจนผ่าน orchestration การ review ของ Main Agent จำกัดอยู่ที่ข้อความสรุป, validation evidence และสถานะ เท่านั้น และไม่นับ raw file content หรือ large binary payload ผลลัพธ์ที่ worker สร้างไฟล์ซึ่ง transport ต้องจัดเก็บให้ส่งต่อเป็น file reference พร้อมชื่อ/ชนิด/ขนาดเท่านั้น ผลลัพธ์ที่ล้มเหลว หยุด หรือยังไม่สมบูรณ์ต้องสามารถ retry ต่อใน worker session เดิมได้ Planner guidance ต้องรอ lifecycle completion event แทนการ polling ซ้ำ ๆ และยังต้องมี explicit status request ให้ใช้ได้ || REQ-025 Transport ที่รับไฟล์ (Discord attachment) ต้องดาวน์โหลดไฟล์นั้นแล้วแปลงเป็น file reference ใน file store และแนบ reference ผ่าน `Input.Metadata` โดยไม่เปลี่ยน canonical Turn/ContentPart contract และต้องคง session routing identity กับ metadata เดิมทั้งหมดไว้ รวมถึง `channel_id` ของต้นทาง ข้อความที่มีเฉพาะ attachment ต้องไม่กลายเป็น turn ว่าง || REQ-026 Worker agent ต้องมี tool สำหรับอ่านไฟล์จาก file store ภายใน root ที่จำกัดด้วย safePath/withinRoot discipline ของ module tool นั้น แล้วสรุปเนื้อหาและสถานะกลับให้ planner ส่วนการส่งไฟล์ออกแบบจริงต้องอยู่ใน transport module เท่านั้น และห้ามส่ง raw bytes หรือ base64 ผ่าน canonical SDK text path || CON-011 File store ของ attachment ต้องอยู่ใต้ state root (`~/.local/share/ai/data/attachments/`) เท่านั้น ไม่ใช่ใน repository/working tree และไม่ใช่ session database; ต้องมีขีดจำกัดขนาดต่อไฟล์/ต่อ session พร้อม TTL cleanup; ห้ามเก็บเนื้อหาไฟล์ใน `data/sessions/` (คง CON-002, CON-003) และ path/limit ต้องกำหนดใน `config/*.json` เท่านั้น (คง CON-001)
Reason: ผู้ใช้ต้องการส่งไฟล์ให้ bot ผ่าน Discord และรับไฟล์ออกแบบกลับได้ แต่ worker เท่านั้นที่ควรเห็นเนื้อหาไฟล์ การให้ Main Agent อ่าน result/history ดิบตาม REQ-019 เดิมจะทำให้ raw file content หรือ base64 payload เข้าไปค้างใน context และ session history ของ planner ซึ่งขัดกับบทบาท planner และทำให้ context บวม จึงจำกัดการ review ของ Main Agent ไว้ที่สรุป, validation evidence และสถานะ แล้วส่งต่อไฟล์เป็น file reference แบบ opaque ที่ worker หรือ transport เท่านั้นที่ resolve ได้
Impact: transport/discord (attachment intake จาก event.Message.Attachments ผ่าน ProxyURL ด้วย http.Client ที่ inject ได้, การส่งไฟล์ออกแบบด้วย ChannelMessageSendComplex/MessageSend.Files, การแก้ DisplayTimeout ไม่ให้ฆ่าอัปโหลดไฟล์ใหญ่); module file store ใหม่ใต้ state root พร้อม manifest/ขนาดจำกัด/TTL; tools.Registry เพิ่ม tool อ่านไฟล์ของ worker ภายใต้ safePath/withinRoot; SDK plan tool allowlist, orchestration result shaping และ sub-agent prompt defaults; runtime configuration ใน config/*.json; cmd/ai wiring; เพิ่ม offline tests (mock RoundTripper/httptest) ไม่มีการเปลี่ยน provider contract, session database layout หรือ canonical Turn/ContentPart contract
Validation: file reference ที่ Main Agent ส่งต่อต้องมีเฉพาะชื่อ/ชนิด/ขนาด/relative path และต้องไม่มีเนื้อหาไฟล์, base64 หรือ MIME data ใน context/history ของ planner; message ที่มีเฉพาะ attachment ต้องไม่กลายเป็น turn ว่าง; channel_id เดิมต้องอยู่ครบหลัง lifecycle continuation; binary (PNG/PDF) ต้องไม่ถูกส่งเป็น raw bytes/base64 เข้า SDK text path; worker อ่านไฟล์ได้เฉพาะภายใน root ของมัน; `go test ./... -timeout 2m`; `go vet ./...`; `bash -n scripts/install.sh`; `bash -n scripts/supervisor.sh`; `git diff --check`; ห้ามใช้ live Discord messages หรือ credentials
Status: accepted

CHANGE-005

Date: 2026-09-15
Type: revise
Request: แก้ไขให้ Main Agent สั่งงานใหม่เข้า worker session เดิมได้ และการดูประวัติต้องเห็นว่าใช้ tools อะไรพร้อมรายละเอียดและผลลัพธ์
Conflict: REQ-019 และ REQ-020 (REQ-019 เดิมอนุญาตเฉพาะ retry งานที่ล้มเหลว/หยุด/ไม่สมบูรณ์ใน session เดิม ไม่ครอบคลุมงานใหม่; REQ-020 เดิมระบุแค่ History/status/stop/follow-up/acceptance แต่ไม่ได้กำหนดว่า history ต้องมีรายละเอียด tool และไม่ได้กำหนด continue สำหรับงานใหม่ใน session เดิม)
Previous: REQ-019 ผลลัพธ์ที่ล้มเหลว หยุด หรือยังไม่สมบูรณ์ต้องสามารถ retry ต่อใน worker session เดิมได้ || REQ-020 ครอบคลุม investigation, planned work และ follow-up; History, status, stop, follow-up และ acceptance ต้องตรวจสอบ ownership ของ parent
New: REQ-019 Main Agent ต้องอ่านประวัติการใช้ tool (ชื่อ tool, arguments/รายละเอียด, ผลลัพธ์รวม error flag) ประกอบการ review; งานใหม่ต้องสั่งต่อเข้า worker session เดิมได้โดยคงประวัติ session เดิม || REQ-020 ครอบคลุม follow-up และ continue; History ต้องแสดงรายการ tool พร้อมชื่อ/arguments/ผลลัพธ์/error; Continue ต้อง reuse worker session เดิม (workerID/session database เดิม) ได้แม้ job ก่อนหน้าถูก accept แล้ว โดยห้ามมี running job ซ้อนกันและต้องผูกกับ plan step ปัจจุบันหรือเป็น investigation
Reason: ผู้ใช้ต้องการสั่งงานต่อเนื่องใน context เดิมของ worker โดยไม่เสียประวัติ และต้องการตรวจสอบว่า worker ใช้ tools ใด อย่างไร ได้ผลอย่างไร จาก history เพียงอย่างเดียว
Impact: sdk/subagent.go (job tool trace capture, History/Status shaping, Continue + continue_subagent tool, planning guidance), sdk/routing.go (reservation สำหรับ continue), sdk/plan_tool.go (allowlist + system prompt)
Validation: follow-up เดิมสำหรับงานล้มเหลว/blocked ยังต้องผ่าน; continue หลัง accept ต้อง reuse workerID เดิมและคงประวัติ worker; concurrent continue/delegate ต้องถูกจองกัน; cross-parent continue/status/history ต้องถูกปฏิเสธ; history ต้องมีชื่อ tool + arguments + result/error และ result สุดท้าย; `go test ./sdk -timeout 2m`; `go test ./... -timeout 2m`
Status: accepted
