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

CHANGE-006

Date: 2026-09-15
Type: revise
Request: งานง่าย ๆ (เช่น สร้างไฟล์ test.txt แล้วลบ) ใช้ parent 30 turns และ worker เกือบ 30 tool calls เกินความจำเป็น ขอให้ทำงานได้สัดส่วนกับความยากของงาน
Conflict: REQ-016 (ค่าเริ่มต้นเดิมบังคับให้ Main Agent มอบหมายการตรวจสอบโปรเจคก่อนเสมอ แม้แต่งานที่ไม่ต้องใช้ repository context) และ REQ-017 (คำสั่ง worker เดิมสั่งให้ validate แต่ไม่จำกัดว่าแค่พอพิสูจน์ผล ทำให้ worker รัน ls/wc/cat/stat ซ้ำไฟล์เดียวกันและลองสูตรคำสั่งหลายแบบ)
Previous: REQ-016 ค่าเริ่มต้นของคำสั่ง Main Agent ต้องมอบหมายการตรวจสอบโปรเจคและการดำเนินงาน แทนการสั่งให้ Main Agent ใช้ worker tools โดยตรง || REQ-017 ค่าเริ่มต้นของคำสั่ง sub-agent ต้องจำกัดงานให้อยู่ใน scope ที่ได้รับ ห้าม delegation ต่อ และห้ามสื่อสารกับ end user โดยตรง และต้องรายงานสิ่งที่ตรวจพบ/ผลลัพธ์ให้ planner
New: REQ-016 ความพยายามต้องได้สัดส่วนกับความซับซ้อน: มีขั้นตอน investigation แยกเฉพาะงานที่ต้องใช้ repository context; งานเล็กน้อยที่ไม่ต้องใช้ repository context ใช้แผนขั้นเดียวที่สั้นที่สุดโดยข้าม investigation แยก; แผนทุกขนาดมี step น้อยที่สุดที่ครอบคลุมเป้าหมาย || REQ-017 การตรวจสอบผลต้องใช้วิธีน้อยที่สุดแต่เพียงพอ: คำสั่งเดียวที่พิสูจน์ผลได้ ห้ามทำซ้ำรายการเทียบเท่าเมื่อพิสูจน์ได้แล้ว และห้ามลองสูตรคำสั่งแบบอื่นต่อหลังสำเร็จแล้ว
Reason: กรณีจริง channel 1549251007995846676 งานสร้าง+ลบไฟล์เดียวเสีย investigation 1 รอบ (8 read-only tools), ตรวจซ้ำด้วย ls/wc/cat/stat หลายรอบ และเสีย 3 calls ไปกับการลองรูปargs ของ run_command ที่รันตรงโดยไม่มี shell; acceptance gating ตาม REQ-019/020 ยังคงเดิม แค่ลดจำนวนรอบที่ไม่จำเป็น
Impact: sdk/plan_tool.go (planning instruction เพิ่ม proportionality), sdk/subagent.go (worker default prompt เพิ่ม minimal validation), cmd/ai/main.go (default prompt ให้ข้าม investigation เมื่องานไม่ต้องใช้ repo context), tools/registry.go (run_command description เตือนกับดัก direct-exec ไม่มี shell พร้อมตัวอย่าง)
Validation: ประโยคบังคับเดิมของ prompt/guidance tests ต้องยังอยู่ครบ (poll/event/follow-up/continue/accept/NOT verified success); prompt ใหม่ต้องมีข้อความ proportionality/minimal-check; run_command description ต้องเตือน direct-exec; `go test ./... -timeout 2m`; `go vet ./...`; `git diff --check`
Status: accepted

CHANGE-007

Date: 2026-09-15
Type: add
Request: เพิ่มคำสั่ง /new สำหรับ Discord ให้สร้าง channel ใหม่พร้อมวันที่และเวลา คัดลอกการตั้งค่าโมเดลจากช่องที่ส่งคำสั่ง และส่งข้อความสรุปการตั้งค่าเข้าช่องใหม่
Conflict: none (เพิ่มคำสั่งใหม่ใน Discord module ตาม REQ-002/015/022; ไม่แตะ core orchestration, persistence, canonical contract หรือ session database layout)
Previous: Discord มีเฉพาะคำสั่ง model/provider/session/stop; การเปิดช่องคุยใหม่ต้องสร้าง channel เองแล้วตั้งค่าโมเดลซ้ำด้วย /model ทุกครั้ง
New: REQ-027 คำสั่ง /new สร้าง text channel ใน guild เดียวกัน ชื่อ `ai-YYYY-MM-DD-HHMM` คัดลอก provider/model/temperature/thinking/API pool index จาก session ต้นทางไป session ช่องใหม่ แล้วส่งสรุปการตั้งค่าเข้าช่องใหม่; กรณีผิดพลาดตอบ ephemeral ในช่องเดิมโดยไม่สร้าง session ใหม่
Reason: ผู้ใช้ต้องการแยกบทสนทนาใหม่โดยไม่ต้องตั้งค่าโมเดลซ้ำ และต้องการเห็นทันทีว่าช่องใหม่ใช้การตั้งค่าอะไร
Impact: transport/discord (handler ใหม่ new_channel.go, gateway dispatch + command registration, แยก summary formatter ใช้ร่วมกับ model settings), cmd/ai (wiring handler); ไม่แตะ sdk core, session persistence, provider contract
Validation: ชื่อช่องต้องตรงรูปแบบวันที่-เวลาและใช้ตัวอักษรที่ Discord อนุญาต; settings ทุก field (รวม key index) ต้องถูกคัดลอกครบ; ข้อความในช่องใหม่ต้องมี provider/model/thinking/temperature/pool; กรณี DM/ไม่มี settings/สร้างช่องล้มเหลวต้อง error แบบ ephemeral และไม่สร้าง session; offline tests ด้วย fake Discord interface เท่านั้น ห้ามใช้ live Discord; `go test ./transport/discord -timeout 2m`; `go test ./... -timeout 2m`; `go vet ./...`; `git diff --check`
Status: accepted

CHANGE-008

Date: 2026-09-15
Type: revise
Request: ปรับ /model เป็น modal 100% — /model แสดง modal เลือก provider เมื่อ submit ให้แสดง modal เลือก model พร้อม temperature/thinking/api pool
Conflict: none (flow เดิมเป็น message-component ที่ไม่มี requirement ล็อกไว้; อยู่ใน Discord module ตาม REQ-015/022; ไม่แตะ core orchestration, persistence, canonical contract)
Previous: /model ตอบกลับเป็น ephemeral message ที่มี provider select menu → เลือกแล้วตอบกลับเป็น ephemeral message ที่มี model select menu แบบแบ่งหน้า (follow-up หลายข้อความเมื่อ model เยอะ) → เลือก model แล้วจึงเปิด modal ขั้นสุดท้าย; มี providerSelectionModal ที่เป็น dead code
New: /model เปิด modal ขั้นที่ 1 ทันที (provider select + ช่อง filter model แบบ optional) → submit แล้วเปิด modal ขั้นที่ 2 (model select สูงสุด 2 เมนู 50 models + temperature + thinking + API pool รวมไม่เกิน 5 components ตามลิมิต modal) → submit แล้ว apply settings และตอบสรุปแบบ ephemeral; ตัด message-component path และ helpers ที่ตายแล้วออก
Reason: ลดจำนวนข้อความ ephemeral หลายชั้นและ follow-up แบ่งหน้า เหลือ modal 2 ขั้นตอนเดียวจบ; filter ช่วยเลือก model จาก catalogue ขนาดใหญ่โดยไม่ต้องไล่เมนูยาว
Impact: transport/discord/model_settings.go (flow + modal builders + submit logic), model_settings_test.go (เขียนใหม่ตาม flow ใหม่); ไม่แตะ gateway dispatch contract, cmd/ai wiring, sdk core
Validation: modal ขั้นที่ 1 มี provider select ครบ + filter optional; modal ขั้นที่ 2 มี model ไม่เกิน 50 ตัว + temp/thinking/key พร้อมค่าเดิม; filter ตรง/ไม่ตรง/เกินลิมิตต้องจัดการถูก; submit ตรวจ model ใน catalogue + ตรวจ temperature/thinking/key ผิดพลาด; offline tests เท่านั้น ห้ามใช้ live Discord; `go test ./transport/discord -timeout 2m`; `go test ./... -timeout 2m`; `go vet ./...`; `git diff --check`
Status: accepted

CHANGE-009

Date: 2026-09-15
Type: revise
Request: ทำ defer reply ให้ทุกคำสั่ง Discord ที่ทำงานใช้เวลา เพื่อกัน interaction หมดอายุ (3 วินาที)
Conflict: none (ยกระดับความน่าเชื่อถือของการตอบ interaction ภายใน Discord module ตาม REQ-022/024; ไม่แตะ core orchestration, persistence, canonical contract)
Previous: /new ตอบ ack ตรงหลังทำงานเสร็จ (สร้าง channel + resolve session + ส่งข้อความ) ถ้าเกิน 3 วินาที interaction จะ failed; /model submit ขั้น 2 และ /session list ตอบตรงหลังโหลด catalogue/อ่านรายการ; มีเพียง /provider submit ที่ defer อยู่แล้ว
New: REQ-024 คำสั่งที่ใช้เวลาต้อง defer ephemeral ก่อนเริ่มงาน แล้วส่งผลลัพธ์/ข้อผิดพลาดทาง followup; ข้อยกเว้นคือการเปิด modal (ตอบทันทีเพราะ defer แล้วเปิด modal ต่อไม่ได้) — ครอบคลุม /new, /model submit ขั้น 2, /session list; /provider คงพฤติกรรมเดิมแต่ใช้ helper ร่วมกัน
Reason: งานช้า (สร้าง channel, โหลด catalogue ผ่าน network, เขียน session) เกิน 3 วินาทีได้เมื่อระบบหน่วง ทำให้ผู้ใช้เห็น interaction failed ทั้งที่งานอาจสำเร็จไปแล้ว
Impact: transport/discord (ไฟล์ใหม่ interactions.go รวม helper defer/followup, new_channel.go, model_settings.go submit ขั้น 2, session_command.go รายการ session, provider_settings.go ใช้ helper ร่วม); ไม่แตะ gateway dispatch contract, cmd/ai wiring, sdk core
Validation: defer ต้องเกิดก่อนงานช้าเสมอ ผลลัพธ์/error หลัง defer ต้องไปทาง followup (ไม่ใช่ InteractionRespond ซ้ำ); modal-open path ต้องยังตอบทันทีแบบเดิม; offline tests ด้วย fake interaction API เท่านั้น ห้ามใช้ live Discord; `go test ./transport/discord -timeout 2m`; `go test ./... -timeout 2m`; `go vet ./...`; `git diff --check`
Status: accepted

CHANGE-010

Date: 2026-09-15
Type: revise
Request: ใช้ /model แล้ว modal แสดงแต่กด submit ขึ้นผิดพลาด (บอทยังรันอยู่)
Conflict: CHANGE-008 (flow สอง modal ต่อกันทำไม่ได้จริงบน Discord API)
Previous: CHANGE-008 /model เปิด modal ขั้นที่ 1 (provider+filter) แล้ว submit เปิด modal ขั้นที่ 2 (model+temperature/thinking/pool)
New: /model เปิด modal เดียวจบ 5 components พอดีลิมิต (provider select, model text input รับ exact ID หรือ unique substring, temperature text, thinking select, API pool text เฉพาะตัวเลข/ว่างคือคงเดิม) → submit แล้ว defer, ตรวจ catalogue, apply, ตอบสรุปทาง followup; gateway ต้อง log interaction handler errors ลง ai.log แทนการกลืนเงียบ
Reason: Discord API ไม่อนุญาตให้เปิด modal เพื่อตอบ modal submit ("Modals can not be sent when responding to a modal") ทำให้ submit ขั้นที่ 1 ถูกปฏิเสธทุกครั้ง; นอกจากนี้ gateway กลืน error ของ handler เงียบจน ai.log ไม่มีร่องรอย ทำให้วินิจฉัยไม่ได้
Impact: transport/discord/model_settings.go (modal เดียว + smart model resolution + submit ใหม่), model_settings_test.go, gateway.go (log handler errors); ไม่แตะ cmd/ai wiring, sdk core, canonical contract
Validation: modal มีครบ 5 fields พร้อม preselect ค่าเดิม; model รับ exact ID และ unique substring, ปฏิเสธชื่อกำกวมพร้อมรายชื่อ และชื่อที่ไม่มีพร้อม error ชัดเจน; key ว่างคง index เดิม; handler error ต้องปรากฏใน log; offline tests เท่านั้น ห้ามใช้ live Discord; `go test ./transport/discord -timeout 2m`; `go test ./... -timeout 2m`; `go vet ./...`; `git diff --check`
Status: superseded by CHANGE-011

CHANGE-011

Date: 2026-09-15
Type: revise
Request: เปลี่ยน /model เป็นข้อความปกติ ใช้ select menu ทั้งหมด และอัพเดทข้อความเดิมแทนการส่งใหม่ซ้ำ ๆ
Conflict: CHANGE-010 (ยกเลิก modal เดี่ยว; catalogue ขนาดใหญ่พิมพ์ชื่อ model เองไม่สะดวก)
Previous: CHANGE-010 /model เปิด modal เดี่ยว (provider select, model text, temperature, thinking, API pool text) แล้ว defer + followup สรุป
New: /model ตอบข้อความปกติในช่อง (regular message ไม่ใช่ ephemeral เพราะ ephemeral แก้ไขไม่ได้) ที่มี provider select → ทุกขั้นถัดไปใช้ deferred-update + แก้ไขข้อความเดิม (provider → model พร้อมปุ่ม pager Prev/Next → temperature presets → thinking → API pool → สรุป) พร้อมปุ่ม Back ย้อนขั้น; เมนู model แต่ละเมนูใช้ custom ID ของตัวเอง (`model:model:N`) เพราะ Discord reject ข้อความที่ custom ID ซ้ำกัน; settings ทั้งหมด apply ครั้งเดียวตอนยืนยันขั้นสุดท้าย; pending state เก็บ process-local keyed ด้วย channel+user
Reason: เลือก model จากรายการดีกว่าพิมพ์ชื่อเองเมื่อ catalogue มีหลายร้อย models; ข้อความเดียวที่อัพเดทตลอดลด spam และเห็นสถานะปัจจุบันเสมอ; deferred-update + edit-original เลี่ยง 3s timeout ทุกขั้นโดยไม่มี loading state ค้าง
Impact: transport/discord/model_settings.go (wizard + pending store + render/step functions), model_settings_test.go, interactions.go (เพิ่ม InteractionResponseEdit ใน interface); ไม่แตะ gateway dispatch, cmd/ai wiring, sdk core, canonical contract
Validation: ทุกขั้นต้อง defer-update ก่อนงานแล้ว edit ข้อความเดิม (ไม่มีข้อความใหม่); pager ครอบคลุม catalogue >125; Back ย้อนขั้นได้; pending หมดอายุ/ข้ามช่องต้อง error ชัดเจน; apply ครั้งเดียวครบทุก field; offline tests ด้วย fake เท่านั้น ห้ามใช้ live Discord; `go test ./transport/discord -timeout 2m`; `go test ./... -timeout 2m`; `go vet ./...`; `git diff --check`
Status: accepted

CHANGE-012

Date: 2026-09-15
Type: revise
Request: /model เป็นข้อความเดียวแบบ control panel 5 แถว (ปุ่ม main/sub agent, provider menu, model menu, ปุ่ม thinking+temperature, pool menu); ถ้า provider/model เกิน 25 ให้ใส่ 24+next / prev+items+next ในตัวเลือกเอง
Conflict: CHANGE-011 (ยกเลิก wizard หลายขั้นแบบ staged; ทุก control บันทึกทันที ไม่มี Back/pending choices เหลือแค่ page state)
Previous: CHANGE-011 wizard staged provider→model(pager)→temp→thinking→key→summary apply ครั้งเดียวตอนจบ
New: REQ-028 panel ข้อความปกติข้อความเดียวแก้ inplace ทุกคลิก (deferred-update + edit-original): ปุ่ม Main/Sub สลับ agent mode ของ session; provider/model select แบ่งหน้าใน options (sentinel `__panel_next__`/`__panel_prev__` เช็คก่อน validate membership); ปุ่ม Thinking วน default→none→low→medium→high, ปุ่ม Temp วน default→0.0→0.1→...→2.0 (label แสดงค่าปัจจุบัน); pool select; เปลี่ยน provider แล้วคง model เดิมถ้ายังอยู่ใน catalogue ไม่เช่นนั้นใช้ตัวแรก; REQ-029 `SessionConfig.AgentMode` persist + `SetAgentMode`, sdk เลือก planning ต่อ session (`sub` = execution tools เต็ม ไม่ wrap planning prompt) แทน global switch อย่างเดียว
Reason: ควบคุมทุกอย่างจบในข้อความเดียว ไม่ต้องไล่หลายขั้น; catalogue ใหญ่แค่ไหนก็อยู่ใน 5 rows เพราะ page controls อยู่ใน options; agent mode ต่อ channel ไม่ต้องแก้ global config
Impact: sdk/types.go (AgentMode), sdk/session_settings.go (SetAgentMode), sdk/agent.go (planningFor ต่อ session 4 จุด), transport/discord/model_settings.go (rewrite เป็น panel), model_settings_test.go, transport/discord/new_channel.go (copy agent mode ไปช่องใหม่); ไม่แตะ gateway dispatch, cmd/ai wiring (นอกจาก handler เดิม), canonical contract
Validation: panel เปิดด้วย deferred channel message + edit (ข้อความเดียวเสมอ); custom ID ไม่ซ้ำในข้อความ; sentinel ไม่ถูกบันทึกเป็นค่า; nav ครอบคลุม >25 providers/models; cycle thinking/temp ครบทุกลำดับและ persist; provider switch คง/รีเซ็ต model ถูกต้อง; sub session ได้ full tools + prompt ไม่ถูก wrap (stream/non-stream); main คงพฤติกรรมเดิม; offline tests ด้วย fake เท่านั้น ห้ามใช้ live Discord; `go test ./transport/discord -timeout 2m`; `go test ./... -timeout 2m`; `go vet ./...`; `git diff --check`
Status: accepted

CHANGE-013

Date: 2026-09-15
Type: revise
Request: provider ไม่ preselect; ปุ่ม thinking/temperature กดแล้วแสดง select menu แทนการวนค่า; model pager เป็นปุ่ม Previous/Next อยู่บนสุดพร้อมตัวบอกหน้า (1/2)
Conflict: CHANGE-012 (ยกเลิก thinking/temp แบบวนค่า และ model แบ่งหน้าใน options)
Previous: CHANGE-012 panel 5 แถว, thinking/temp วนค่าด้วยปุ่ม, model/provider แบ่งหน้าใน options ด้วย sentinel
New: REQ-028 แถวแรกเป็นปุ่ม `[Main agent] [Sub agent]` ต่อด้วยปุ่ม pager `[◀] [(p/n)] [▶]` เฉพาะเมื่อ model มีหลายหน้า (ปุ่มหน้าปิด disabled, ปุ่ม (p/n) disabled เสมอ รวมไม่เกิน 5 ปุ่มต่อแถว); provider menu ไม่ preselect; model menu แสดง 25 รายการต่อหน้าเต็มโดยไม่มี nav options; ปุ่ม Thinking/Temp กดแล้วแถวเดียวกันกลายเป็น select menu (thinking ใช้รายการเดิม, temp มี default + 0.0–2.0 22 options, preselect ค่าปัจจุบัน) เลือกแล้วบันทึกและแถวกลับเป็นปุ่ม, กดปุ่มเดิมซ้ำคือยกเลิก; provider ยังแบ่งหน้าใน options เหมือนเดิม; เลือก control อื่นปิด selector ที่เปิดอยู่
Reason: ไม่ preselect provider เพื่อไม่ชี้นำค่า; select menu เลือก thinking/temp เร็วกว่ากดวน 22 ครั้ง; pager บนสุดเห็นก่อนและรู้ว่าอยู่หน้าไหน
Impact: transport/discord/model_settings.go (selector state, pager row, thinking/temp select), model_settings_test.go; ไม่แตะ sdk, gateway dispatch, /new, canonical contract
Validation: เปิด panel ได้ 5 แถวเสมอ (หน้าเดียวไม่มี pager); pager เปลี่ยนหน้าไปกลับ + disabled ถูกข้าง + (p/n) ตรง; thinking/temp select บันทึกค่าและกลับเป็นปุ่ม; provider ไม่มี default; model หน้าเต็ม 25 ไม่มี sentinel; offline tests ด้วย fake เท่านั้น ห้ามใช้ live Discord; `go test ./transport/discord -timeout 2m`; `go test ./... -timeout 2m`; `go vet ./...`; `git diff --check`
Status: accepted

CHANGE-014

Date: 2026-09-15
Type: revise
Request: pager ไม่ใช่ปุ่ม แต่เป็นตัวเลือกที่ 1–2 (Previous/Next) ในเมนู model และ (1/2) อยู่ในชื่อเมนู Select model (1/2); ปุ่ม thinking/temperature กดแล้วเปิด modal (thinking เป็น select menu, temperature กรอกตัวอักษร) submit แล้วอัพเดทข้อความเดิม
Conflict: CHANGE-013 (ยกเลิก pager แบบปุ่มบนแถวแรก และ thinking/temp แบบ select ในข้อความ)
Previous: CHANGE-013 pager เป็นปุ่มบนแถว agent, thinking/temp กดแล้วกลายเป็น select ในข้อความ
New: REQ-028 แถวแรกเหลือปุ่ม agent ล้วน (คง handler ปุ่ม pager เก่าไว้ให้ข้อความ v1.61 กดต่อได้); model catalogue เกิน 25 ตัวเลือกที่ 1–2 คือ Previous/Next เสมอ + models สูงสุด 23 ตัว (รวมไม่เกิน 25) ชื่อเมนูบอกหน้า `Select model (p/n)` หน้าเดียวไม่มี nav; model เลิก preselect; ปุ่ม Thinking/Temp เปิด modal เดียวกัน (thinking select preselect ค่าปัจจุบัน + temperature text prefill ค่าปัจจุบัน) submit แล้ว validate + apply + ตอบ InteractionResponseUpdate แก้ panel เดิม (catalogue ใช้ cache อยู่แล้ว) ค่าผิดตอบ ephemeral error; ลบ selector state ทั้งหมด
Reason: pager ใน options ไม่เปลืองแถวและเห็นตำแหน่งพร้อมรายการ; modal กรอก temp เร็วกว่าไล่ select 22 options และพิมพ์ทศนิยมอิสระได้ในกรอบ 0.0–2.0
Impact: transport/discord/model_settings.go (model options paging, modal open/submit, ลบ selector), model_settings_test.go; ไม่แตะ sdk, gateway dispatch, /new, canonical contract
Validation: หน้าเดียว/หลายหน้า options ถูก (nav อยู่ 1–2 เสมอ รวมไม่เกิน 25); placeholder มี (p/n); ไม่ preselect; modal fields ครบ + prefill ตรง; submit บันทึกทั้งสองค่าและแก้ panel เดิม; ค่าผิด error ชัดเจน; ปุ่ม pager เก่ายังใช้งานได้; offline tests ด้วย fake เท่านั้น ห้ามใช้ live Discord; `go test ./transport/discord -timeout 2m`; `go test ./... -timeout 2m`; `go vet ./...`; `git diff --check`
Status: accepted

CHANGE-015

Date: 2026-09-15
Type: revise
Request: model pager หน้าแรกไม่ต้องมี Previous หน้าสุดท้ายไม่ต้องมี Next; เพิ่มอิโมจิตกแต่ง
Conflict: CHANGE-014 (nav Previous/Next อยู่ทุกหน้า)
Previous: CHANGE-014 model เกิน 25 ทุกหน้ามี Previous + Next เป็น options 1–2
New: model paging ใช้ scheme เดียวกับ provider (หน้าแรก 24 รายการ + Next, หน้ากลาง Previous + 23 รายการ + Next, หน้าสุดท้าย Previous + รายการที่เหลือ) รวมไม่เกิน 25 options เสมอ; placeholder มี (p/n) เหมือนเดิม; ไม่ preselect เหมือนเดิม; ตกแต่ง panel (หัวข้อ explanation, ปุ่ม agent/thinking/temp, placeholder ทุกเมนู, modal title) ด้วยอิโมจิ โดยค่า/value ไม่เปลี่ยน
Reason: ตัด nav ที่กดแล้วไม่ไปไหนออก; อิโมจิช่วยให้แยก control แต่ละแถวได้เร็ว
Impact: transport/discord/model_settings.go (modelMenuOptions/modelPageFor ใช้ panelPages/panelWindow ร่วมกับ provider, emoji labels), model_settings_test.go; ไม่แตะ sdk, gateway, /new, canonical contract
Validation: หน้าแรกมีแค่ Next + 24 รายการ / หน้ากลางครบ / หน้าสุดท้ายมีแค่ Previous; placeholder (p/n) ตรง; emoji ไม่ทำให้ custom ID/value เปลี่ยน; offline tests ด้วย fake เท่านั้น ห้ามใช้ live Discord; `go test ./transport/discord -timeout 2m`; `go test ./... -timeout 2m`; `go vet ./...`; `git diff --check`
Status: accepted

CHANGE-016

Date: 2026-09-15
Type: revise
Request: Next อยู่ด้านบน (หน้าแรก next + 1-24, หน้ากลาง previous + next + 23 รายการ, หน้าสุดท้าย previous + ที่เหลือ); thinking ใช้ 💭; key pool ไม่ preselect; สรุปเป็น embed
Conflict: CHANGE-015 (Next อยู่ท้ายหน้าแรก; key pool preselect; สรุปเป็นข้อความ)
Previous: CHANGE-015 provider/model paging หน้าแรก 24 รายการ + Next ต่อท้าย, key pool preselect ค่าปัจจุบัน, สรุปเป็น markdown text
New: REQ-028 `pagedOptions` กลาง (ใช้ร่วม provider/model) วาง Next ไว้ options แรกเสมอ (หน้าแรก Next + 24 รายการ หน้ากลาง Previous + Next + 23 รายการ หน้าสุดท้าย Previous + ที่เหลือ รวมไม่เกิน 25); key pool เลิก preselect เหมือนอีกสองเมนู; สรุป settings ใน panel เป็น embed (title + fields Provider/Model/Thinking/Temperature/API Pool/Agent) แทน markdown text, error ยังเป็น content text เหนือ embed; thinking 💭 แทน 🧠 (ปุ่ม + modal title); หมายเหตุตัวอย่างหน้ากลาง 25-48 ของผู้ใช้ปรับเป็น 25-47 เพราะลิมิต 25 options
Reason: nav อยู่ด้านบนเห็นก่อนไม่ต้องเลื่อน; ไม่ preselect ให้เมนูเป็นกลางทุกเมนู; embed อ่านง่ายกว่า text ก้อนเดียว
Impact: transport/discord/model_settings.go (pagedOptions order, makeKeyOptions ไม่ preselect, renderPanel คืน embed, editPanelMessage helper, modal title), model_settings_test.go; ไม่แตะ shared summary (ใช้กับ /new ต่อ), sdk, gateway, canonical contract
Validation: หน้าแรก Next อยู่อันแรก / หน้ากลาง Prev+Next อยู่อันแรก / หน้าสุดท้ายมีแค่ Prev; key pool ไม่มี default; panel edit มี embed ครบ 6 fields ถูกค่า; submit modal อัพเดท embed; error เป็น text; emoji ไม่แตะ ID/value; offline tests ด้วย fake เท่านั้น ห้ามใช้ live Discord; `go test ./transport/discord -timeout 2m`; `go test ./... -timeout 2m`; `go vet ./...`; `git diff --check`
Status: accepted

CHANGE-017

Date: 2026-09-15
Type: revise
Request: panel เป็น Components V2; ปุ่ม Save สีเขียวล่างสุด; สรุปแบ่ง 2 ฝั่งซ้ายขวา main/sub
Conflict: CHANGE-016 (panel เป็น V1 + สรุป embed เดี่ยว)
Previous: CHANGE-016 panel V1 (5 action rows) + สรุป embed + ปุ่ม agent แถวแรก
New: REQ-028 panel ส่ง flag IsComponentsV2 ทุก response/edit (content/embeds เดิมใช้ไม่ได้ใน V2): Container (accent blurple) มี TextDisplay หัวข้อ, TextDisplay ค่า settings, Separator, Section ฝั่ง Main (accessory ปุ่ม Main) + Section ฝั่ง Sub (accessory ปุ่ม Sub) ฝั่ง active ปุ่ม Primary + ข้อความ ✅ Active (settings ชุดเดียวสลับแค่พฤติกรรม); ต่อด้วย ActionRow provider/model/thinking-temp/pool เหมือนเดิม + ActionRow ปุ่ม Save (Success สีเขียว) ล่างสุด; Save = freeze (defer-update + แก้ข้อความเป็น Container สรุปอย่างเดียว ไม่มี controls); modal submit ตอบ Update พร้อม V2 เช่นกัน; ลบ panelSummaryEmbed (V2 ห้าม embeds)
Reason: V2 จัดสรุปกับปุ่มให้อยู่ด้วยกันได้โดยไม่เปลืองแถว; Save ปิดงานกันกดพลาด; สรุปคู่เห็นโหมดทั้งสองพร้อมตัวที่ active
Impact: transport/discord/model_settings.go (V2 builders, save/freeze, ลบ embed), model_settings_test.go (V2-aware helpers); ไม่แตะ shared text summary (/new), sdk, gateway dispatch, modal, canonical contract
Validation: open/defer/edit/submit ทุก response มี V2 flag; โครงสร้าง Container + 5 ActionRows (+Save); Section 2 ฝั่งครบ ปุ่ม active Primary + ✅ ถูกฝั่ง; Save แล้วเหลือ Container เดียวไม่มี controls; modal submit อัพเดท V2; offline tests ด้วย fake เท่านั้น ห้ามใช้ live Discord; `go test ./transport/discord -timeout 2m`; `go test ./... -timeout 2m`; `go vet ./...`; `git diff --check`
Status: accepted

CHANGE-018

Date: 2026-09-15
Type: revise
Request: สรุป panel เป็น 2 บล็อก Main/Sub ค่าอิสระกันตาม layout ที่ผู้ใช้วาด (title, Main 5 บรรทัด, sep, Sub 5 บรรทัด, sep, ปุ่ม main/sub, provider, model, thinking/temp, pool, sep, save); controls แก้ฝั่ง active
Conflict: CHANGE-017 (สรุปคู่ค่าเดียว + sections มีปุ่มข้าง)
Previous: CHANGE-017 container มี sections ฝั่งละปุ่ม, settings ชุดเดียว
New: REQ-030 `ModeSettings` (provider/model/keyIndex/thinking/temperature) + `SessionConfig.Sub` persist; setters เขียนฝั่ง active (`SetKeyPool` คง main เพื่อ caller เดิม, เพิ่ม `SetSubKeyPool`); `EffectiveConfig` overlay เมื่อ sub; turn path ใช้ effective (router provider/model/thinking/temp, `APIKey`/`RotateAPIKey` ใช้ pool+index ฝั่ง active, worker spawn, resp labels); Resolve re-attach pool สองฝั่ง; เข้า sub ครั้งแรก seed จาก main (`EnsureSubSettings`); REQ-028 container เป็น TextDisplay ล้วน (title, main block, sep, sub block) ไม่มี sections, มี sep คั่นก่อน controls และก่อน save, `/new` copy ทั้งสองฝั่ง + สรุป text สองบล็อก
Reason: main/sub ใช้งานจริงคนละ model/pool กัน (เช่น main วางแผน sub ทำงาน) ต้องแยก settings แต่สลับในช่องเดียวได้
Impact: sdk/types.go (ModeSettings), sdk/session_settings.go (setters ฝั่ง active + EnsureSubSettings + SetSubKeyPool), sdk/routing.go (subKeys + EffectiveConfig + APIKey/Rotate), sdk/router_client.go, sdk/agent.go (labels), sdk/subagent.go (inherit effective), runtime/session_manager.go (re-attach), transport/discord/model_settings.go (layout ตาม sketch + active side), model_settings_test.go, sdk/mode_settings_test.go (ใหม่), transport/discord/new_channel.go (copy สองฝั่ง); ไม่แตะ gateway dispatch, modal, canonical contract
Validation: setters เขียนถูกฝั่งตาม mode; effective overlay ครบทุก field; sub turn ใช้ pool/index/model ฝั่ง sub (stream/non-stream); seed ครั้งแรก; restart แล้ว pool สองฝั่งกลับมา; worker inherit effective; panel/controls/modal อ่านเขียนฝั่ง active; summary สองบล็อกค่าถูก; /new copy ครบ; offline tests ด้วย fake เท่านั้น ห้ามใช้ live Discord; `go test ./transport/discord -timeout 2m`; `go test ./... -timeout 2m`; `go vet ./...`; `git diff --check`
Status: accepted

CHANGE-019

Date: 2026-09-15
Type: revise
Request: ปุ่มทั้งหมดอยู่ใน container แถบสีเดียวกัน; สรุปไม่ใส่อิโมจิ อิโมจิอยู่แค่ปุ่ม/เมนู
Conflict: CHANGE-018 (ปุ่มอยู่นอก container; สรุปมีอิโมจิ)
Previous: CHANGE-018 container มีแค่สรุป ปุ่มอยู่ action rows ข้างนอก สรุปมีอิโมจิทุกบรรทัด
New: REQ-028 Container ประกอบด้วยหัวข้อ + บล็อก Main + sep + บล็อก Sub + ปุ่ม 5 ปุ่มในรูปแบบ Section (Main/Sub/Thinking/Temp/Save ข้อความซ้ายปุ่มขวา ปุ่ม active เป็น Primary); select menu (provider/model/pool) อยู่ข้างนอกเป็น top-level ActionRows เพราะ Discord ไม่ยอมรับ select ใน container (ได้เฉพาะปุ่มผ่าน Section accessory); สรุปเป็น plain text ทั้งหมด (หัวข้อ บล็อก โหมด) อิโมจิเหลือแค่ปุ่ม labels, select placeholders และ nav options; modal title เป็น plain
Reason: ปุ่มกับสรุปอยู่ใน accent bar เดียวกันอ่านเป็นกล่องเดียว; สรุป plain อ่านค่าชัด ไม่แย่งซีนกับ controls
Impact: transport/discord/model_settings.go (container builders, modal title), model_settings_test.go; ไม่แตะ sdk, /new logic (shared summary plain ตาม), gateway, canonical contract
Validation: ปุ่มทุกปุ่มอยู่ใน container (sections) + selects ข้างนอก; สรุปไม่มีอิโมจิ; ปุ่ม/placeholder ยังมีอิโมจิ; custom ID/value ไม่เปลี่ยน; Save freeze เหมือนเดิม; offline tests ด้วย fake เท่านั้น ห้ามใช้ live Discord; `go test ./transport/discord -timeout 2m`; `go test ./... -timeout 2m`; `go vet ./...`; `git diff --check`
Status: accepted

CHANGE-020

Date: 2026-09-15
Type: revise
Request: select menu เอาเข้า container ด้วย (ผู้ใช้ทักว่าทำได้)
Conflict: CHANGE-019 (อ้างว่า container รับได้แค่ปุ่มผ่าน Section accessory ซึ่งผิด)
Previous: CHANGE-019 ปุ่มเป็น sections ใน container, selects อยู่ข้างนอก
New: ตรวจสอบ docs แล้ว ActionRow (ปุ่มและ select) อยู่ใน Container ได้จริง ย้าย controls ทั้งหมดเข้า container เดียว: title, main, sep, sub, แถวปุ่ม Main/Sub, provider, model, Thinking/Temp, pool, Save รวม 10 children พอดีลิมิต (ลบ section helpers ที่ไม่ใช้); top-level เหลือ Container เดียว; frozen เหลือ Container สรุป 5 children
Reason: ทั้ง panel อยู่ใน accent bar เดียวกันตามที่ขอตั้งแต่แรก; แก้ข้อมูลผิดใน CHANGE-019
Impact: transport/discord/model_settings.go (render/frozen layout), model_settings_test.go; ไม่แตะ sdk, /new, gateway, modal, canonical contract
Validation: top-level มี Container เดียว; children ครบ 10 ตามลำดับ; custom ID ครบ; V2 flag ครบ; Save freeze เหลือ container เดียว; offline tests ด้วย fake เท่านั้น ห้ามใช้ live Discord; `go test ./transport/discord -timeout 2m`; `go test ./... -timeout 2m`; `go vet ./...`; `git diff --check`
Status: accepted

CHANGE-021

Date: 2026-09-15
Type: revise
Request: main/sub ทำงานไม่ถูก แยกให้ชัด session sub เก็บแยก db reuse ได้ตลอดจนกว่าจะลบ
Conflict: CHANGE-018 (overlay Sub ใน session เดียว ประวัติปนกัน)
Previous: CHANGE-018 main/sub เป็น overlay ใน session เดียว (history เดียว db เดียว)
New: REQ-030 สอง sessions ต่อ channel (`discord:channel:<id>` planner + `discord:channel:<id>:sub` worker db แยก) settings อยู่ top-level ของแต่ละ session ไม่มี overlay; gateway route ข้อความตามโหมด active บน main session (resolve ล้มเหลวใช้ main); revert setters/effective/pool กลับ top-level ทั้งหมด (คง `AgentMode` + `SetAgentMode` + `planningFor` ราย session); panel resolve สอง sessions สรุปสองบล็อก controls/modal แก้ฝั่ง active เข้า sub ครั้งแรก seed จาก main; `/new` คัดลอกสอง sessions + สรุปสองบล็อก; ยังไม่มีคำสั่งลบ session (นอก scope รอบนี้)
Reason: ประวัติการคุยต้องแยกกัน main วางแผน sub ทำงาน ไม่ปน; sub session ต้องอยู่ถาวรเรียกซ้ำได้ไม่ผูกกับ job
Impact: sdk/types.go (ลบ ModeSettings/Sub), sdk/session_settings.go (setters top-level), sdk/routing.go (ลบ subKeys/effective/active pool), sdk/router_client.go, sdk/agent.go, sdk/subagent.go, runtime/session_manager.go (revert re-attach), sdk/mode_settings_test.go (ลบ), sdk/agent_mode_test.go, transport/discord/gateway.go (route ตาม mode), cmd/ai/main.go (wiring), transport/discord/model_settings.go (2 sessions), transport/discord/new_channel.go; ไม่แตะ modal, V2 layout, canonical contract
Validation: main/sub turn ใช้ session/history/db ของตัวเอง; toggle สลับฝั่ง; seed ครั้งแรก; restart แล้วสองฝั่งกลับมา; panel สรุป/controls ถูกฝั่ง; /new copy ครบ; offline tests ด้วย fake เท่านั้น ห้ามใช้ live Discord; `go test ./transport/discord -timeout 2m`; `go test ./... -timeout 2m`; `go vet ./...`; `git diff --check`
Status: accepted

CHANGE-022

Date: 2026-09-16
Type: revise
Request: trace เป็น Components V2, ลำดับ main/sub ถูกต้อง, แสดง tool ทุกตัว, รวม provider accepted เข้ากับ tool, จับเวลาด้วย timestamp subtraction (sending request → tool execute สำเร็จ), plan ใช้ได้จริง (main ถือ checklist/mองภาพรวม, sub ทำงานย่อย, report ครบใน delegation result แบบ blocking, stop เป็น blocking รอผลจริง, ลด API call)
Conflict: REQ-019/020/021 เดิม (delegate async + completion event ฉีดเข้า parent + status/history polling), REQ-022 เดิม (progress เป็น embed), การจับเวลาเดิม (Elapsed = time.Since(turnStart) ณ ตอน emit ฝั่ง sdk)
Previous: trace embed แยกข้อความ main/sub, ซ่อน orchestration tools ในบาง path, บรรทัด "provider accepted" อิสระ, Elapsed นับจาก turn start, delegate คืน job id แล้วรอ event, stop = fire-and-forget
New: REQ-031 trace V2 ข้อความเดียวต่อ channel ต่อ turn เรียงตามเวลาจริง (🤖 planner / 🛠 worker), REQ-032 แสดงทุก tool, REQ-033 เวลา = resultTs - requestSentTs และ acceptedTs - requestSentTs ผ่าน TraceEvent timestamp fields ที่ sdk บันทึกแบบ UnixMilli ลบกัน, provider-accepted รวมเข้ากลุ่ม tool; REQ-034 + แก้ REQ-016/019/020/021/022: checklist ต่อ planner system prompt ทุก iteration, delegate/follow_up/continue/stop เป็น blocking คืนรายงานครบ (status+result+tool history+สรุป) ใน tool result เดียว ตัด subagent_status/subagent_history ออกจาก tool surface, ตัด completion-continuation sink (loop ไม่ฉีด user turn จาก event อีก), worker ต้องจบงานด้วยรายงานที่ planner ตรวจซ้ำเองไม่ต้อง
Reason: main/sub สลับกันแสดงจนอ่านไม่ออก; ซ่อน tool ทำให้ไม่เห็นว่าเกิดอะไรขึ้น; เวลาจาก turn start ทำตัวเลขหลอก; async delegate ทำให้ main ต้อง poll/อ่าน history = API call เปลือง และ stop รายงานทีหลังทำให้สับสน
Impact: sdk/trace.go (RequestStartedMs/ProviderAcceptedMs + elapsed คำนวณจากลบ timestamp), sdk/agent.go (บันทึก timestamp ต่อ request), sdk/subagent.go (blocking wait + report builder + ตัด status/history tools + ตัด completion sink), sdk/subagent_trace_sink.go, sdk/loop.go (ไม่ inject completion), sdk/plan_tool.go (checklist prompt + instructions + allowlist), transport/discord/actor_trace_display.go (V2 เดียว per channel เรียงเวลา), gateway.go (send/edit V2 helpers); ไม่แตะ db schema, session routing, modal, panel, canonical Turn contract
Validation: offline tests เทียบ timestamp ที่ stub ไว้พิสูจน์การลบ timestamp; delegate ใน test คืน report หลัง worker จบจริง; stop block จน status=stopped แล้วคืนรายงาน; ไม่มี tool names subagent_status/subagent_history; planner prompt มี checklist สถานะ; trace render เป็น Container/TextDisplay + V2 flag, sub lines ใต้ main lines ตามลำดับ, ไม่มี "provider accepted" บรรทัดค้าง; `go test ./... -timeout 2m`; `go vet ./...`; `git diff --check`
Status: accepted

CHANGE-023

Date: 2026-09-16
Type: revise
Request: from log channel 1549609343190827231 — content ที่มาพร้อม tool call แสดงผิดลำดับ; กลับแยกสี main/sub แบบเดิมแต่เรียงลำดับให้ถูก; แสดง args ของ tool
Conflict: CHANGE-022 (รวมทุก actor เป็นข้อความเดียวต่อ channel; trace ไม่แสดง args)
Previous: trace V2 single-stream per channel, ทุก actor บรรทัดรวมกัน, tool line มีเฉพาะชื่อ+เวลา
New: REQ-031 กลับเป็นข้อความต่อ actor (planner accent blurple, worker accent เขียว) พร้อม global per-channel message sequence: เมื่อมีการสร้างข้อความใหม่ใดๆ ใน channel (อีก actor หรือ response text) actor ที่มี anchor เก่ากว่าต้องเปิด segment ใหม่ด้านล่างเสมอ บรรทัดใหม่ห้ามเด้งกลับไปเหนือข้อความที่ใหม่กว่า; REQ-032 tool line แสดง arguments one-line ตัดที่ 120 runes (args = ยอมให้โชว์ได้, result raw = ห้ามเหมือนเดิม)
Reason: content ที่ emit พร้อม response เดียวกับ tool calls ถูกส่งทันที ทำให้ trace บรรทัดหลังจากนั้นไปต่อในข้อความเก่าเหนือ content — อ่านสับสน; ผู้ใช้ยืนยันแยกสีอ่านง่ายกว่า; args จำเป็นต่อการดูงานจริง
Impact: transport/discord/actor_trace_display.go (per-actor state + channel sequence + segment split + args), gateway.go ไม่แตะ, adapter.go legacy formatters เพิ่ม args, actor_trace_display_test.go, rendering_test.go; ไม่แตะ sdk, orchestration, canonical contract
Validation: tests พิสูจน์: worker message ถูกสร้างหลัง main message; main result line หลัง worker completion อยู่ segment ใหม่ใต้ worker message; content กลางคันทำให้ tool line ถัดไปเปิด segment ใหม่; tool line มี args แต่ result text ไม่หลุด; สี accent two values; offline tests เท่านั้น; `go test ./transport/discord -timeout 2m`; `go test ./... -timeout 3m`; `go vet ./...`; `git diff --check`
Status: accepted

CHANGE-024

Date: 2026-09-16
Type: revise
Request: เปลี่ยน run_command เป็น bash ใช้คำสั่งตรง; provider accepted เมื่อ content มาถึงให้ลบออกจาก container หรือถ้ามีมันอย่างเดียวให้แปลงเป็น content; main agent เป็น planner ไม่ใช่ผู้ส่งต่อคำสั่ง; main↔sub สื่อสารภาษาอังกฤษ
Conflict: REQ-017 เดิม, `run_command` ใน registry (command+args schema), REQ-031/033 เดิม (provider accepted ค้างใน trace)
Previous: run_command ต้องแยก command/args หรือมี shell syntax ถึงจะรันแบบ shell; accepted line ค้างเมื่อ response ไม่มี tool; planner prompt ไม่ห้ามการส่งต่อข้อความดิบ;ภาษาของ orchestration ตามผู้ใช้
New: REQ-035 tool ชื่อ `bash` schema {"command": string, "timeout_ms"?} รันผ่าน bash เสมอ; REQ-036 planner role: วางแผน-เขียน task ภาษาอังกฤษมีบริบท ห้าม relay ดิบ; REQ-037: content ถึง → accepted เดี่ยว = แปลงข้อความเดิมเป็น content (edit), accepted + บรรทัดอื่น = ลบ accepted แล้วส่ง content ข้อความใหม่; worker prompt + delegation guidance ระบุ English-only ระหว่าง agent (REQ-017 แก้)
Reason: AI พิมพ์ `ls -la /x` ผิดรูปแบบแล้ว fail เสียรอบ; accepted ค้างทำให้ trace เลอะ; relay ดิบทำให้ worker ทำงานไม่ตรงเป้า; TH↔EN สลับกันเปลือง token
Impact: tools/registry.go + tools/command.go (bash tool, schema, description), tests ใน tools/ และผู้ใช้ชื่อ tool ใน sdk tests, transport/discord actor trace + rendering tests, sdk/plan_tool.go (instructions), sdk/subagent.go (worker prompt + report), cmd/ai/main.go (defaultSystemPrompt); ไม่แตะ db, gateway routing, canonical contract
Validation: bash tool รัน "ls -la /x" ตรง ๆ สำเร็จใน tests; provider accepted เดียวถูก edit เป็น content (ไม่มีข้อความใหม่), กรณีมี tool line อื่น accepted ถูก delete ก่อน content; prompt มี English-only + ห้าม relay; offline tests เท่านั้น; `go test ./... -timeout 3m`; `go vet ./...`; `git diff --check`
Status: accepted

CHANGE-025

Date: 2026-09-16
Type: revise + add
Request: delegate ต้องคืน shell ให้ main ทันที; ทุก X tool calls ของ sub ให้รายงานเข้า main เพื่อตรวจ scope (ขาด/เกิน); เพิ่ม /workspace ใน Discord เลือกตำแหน่งทำงาน รองรับ ~/
Conflict: CHANGE-022/REQ-019/021/034 (delegation แบบ blocking ทั้งหมด; ห้ามมี continuation turn); tools registry ใช้ root ถาวรต่อ process
Previous: delegate/follow/continue block จน worker จบ; ไม่มีการรายงานระหว่างทาง; workspace เป็นค่าเดียว global (AI_WORKSPACE/home), agent_mode อ้าง persist แต่ไม่เคยถูกเขียนลง DB
New: REQ-019/021/034 แก้ — delegate/follow/continue คืน job id ทันที; progress report ทุก X tool calls (X = `sub_agent.report_every_tool_calls`, ค่าเริ่มต้น 5) และ final report ถูก inject เป็น continuation turn ของ main เท่านั้น (2 ชนิด); stop ยัง blocking (REQ-020 คงเดิม) — Main ตรวจ scope จาก progress, เกิน/ออกนอกขอบเขต = stop + follow_up; REQ-038 ใหม่ — `/workspace <path>` ต่อ channel (set ทั้ง main+sub), expand `~/`+`~`, ต้องเป็น directory มีอยู่จริง, persist คอลัมน์ `workspace`, tools resolve root ต่อ invocation ตาม session ใน ctx (fallback global), `/new` copy; bug fix: SaveSession/LoadSession เพิ่มคอลัมน์ `agent_mode` (migration ผ่าน ensureSessionColumns เดิม) ให้ตรง REQ-029/030 ที่ระบุไว้แล้ว
Reason: main ที่ block ใน delegate รับข้อความผู้ใช้ใหม่ไม่ได้และมองงานไม่ระหว่างทาง; scope drift ต้องถูกจับ early; งานหลายโปรเจคต้องรันในหลายตำแหน่งจาก bot ตัวเดียว
Impact: sdk (types.go Workspace, session_db.go workspace+agent_mode persist, session_settings.go SetWorkspace, subagent.go async+progress reports+worker workspace inheritance, loop.go sinks, context helper WithWorkspace, plan_tool instructions), runtime (system_config report_every_tool_calls, session_manager WorkspaceFor), tools (registry resolver, file/bash/job handlers resolve root ต่อ ctx), transport/discord (workspace.go ใหม่, gateway register+route, new_channel copy), tests ทุกชั้นที่เกี่ยวข้อง
Validation: delegate คืนทันทีเมื่อ worker ยังรัน; progress event มาทุก 5 tool calls; final report inject ครบ; stop block จน stopped; SetWorkspace persist ผ่าน restart (ทั้ง main+sub + agent_mode); bash/read_file ใน session ที่มี workspace的不同 ทำงานคนละ root; /workspace expand ~/ และ reject path ไม่มีอยู่; offline tests เท่านั้น; `go test ./... -timeout 3m`; `go vet ./...`; `git diff --check`
Status: accepted

CHANGE-026

Date: 2026-09-16
Type: add
Request: เพิ่ม adapter สำหรับ opencode ให้มี header และ session id เสมือนว่าใช้ผ่าน opencode โดยตรง (แยก adapter ไม่รวมกับ AgentRouter); อนุญาตให้ยิง API ทดสอบด้วย key ที่ให้มา โมเดล free ใดก็ได้ ที่ endpoint opencode.ai/zen/v1/
Conflict: none (adapter ใหม่ ไม่เปลี่ยนพฤติกรรม adapter เดิม; name inference เดิมคงไว้)
Previous: มี adapter แค่ openai/anthropic/gemini; ไม่มี fingerprint ของ opencode; Request ไม่มี session identity; provider นอก opencode เรียก free tier ของ Zen ไม่ได้ (MissingSessionID) และไม่ได้โควต้าถูก bucket (FreeUsageLimitError เมื่อขาด User-Agent)
New: REQ-039 — adapter `opencode` (OpenAI-compatible `/chat/completions`): ส่ง `User-Agent: opencode/<version>`, `HTTP-Referer: https://opencode.ai/`, `X-Title: opencode`, `x-opencode-session: ses_f<hex8>ffe<rand14>` ทุก request; ses_-id mint ครั้งเดียวต่อ harness session (cache ใน memory); custom headers ชนะ UA/Referer/Title ได้แต่ชนะ session header ไม่ได้; body ไม่มี `user`; BaseURL เริ่มต้น `https://opencode.ai/zen/v1`; เลือกผ่าน `"adapter": "opencode"` explicit เท่านั้น; RouterClient stamp `Request.SessionID` จาก session ใน Generate/Stream
Reason: ตรวจจาก opencode 1.18.31 บนเครื่อง: provider ที่ id ขึ้นต้นด้วย opencode ส่ง headers ชุดนี้ (x-opencode-session/sessionID, x-opencode-request/user.id, x-opencode-client/flags.client, User-Agent) และ free tier ของ Zen ตรวจ session (`MissingSessionID` ถ้าไม่มี) กับ quota bucket จาก User-Agent (`FreeUsageLimitError` ถ้า UA ไม่ใช่ opencode) — ทดสอบ live แล้ว: session+UA ผ่าน, ขาดอย่างใดอย่างหนึ่งติด error
Impact: sdk (types.go AdapterOpenCode + Request.SessionID, router_client.go stamp SessionID, providers/opencode แพ็กเกจใหม่), runtime (runtime.go register, provider_manager.go Adapters+ensureAdapter, provider_config.go explicit adapter), transport/discord (provider settings options 3→4), tests ทุกชั้นที่เกี่ยวข้อง
Validation: unit (format/stability/uniqueness ของ ses_-id, headers ครบ, ไม่มี user ใน body, parse chat+tools, ListModels ผ่าน httptest, runtime registration) + live 1 call ผ่าน adapter จริงกับ mimo-v2.5-free; `go test ./... -timeout 3m`; `go vet ./...`; `git diff --check`
Status: accepted

CHANGE-027

Date: 2026-09-16
Type: add
Request: ตรวจ log ของ channel 1549723143218663434 ที่ยัง error; เพิ่ม provider https://inference-api.nousresearch.com/v1/ ไม่ได้
Conflict: none (พฤติกรรมเสริม ไม่เปลี่ยน wire format หรือ semantics เดิม)
Previous: provider HTTP ใช้ Go default UA (`Go-http-client/1.1`); provider-settings error ส่งข้อความเต็มขึ้น Discord โดยไม่ตัด
New: REQ-040 — `User-Agent: ai` เริ่มต้นทุก provider request (adapter/config ชนะได้); error ของ provider settings ถูก truncate ให้อยู่ใน limit Discord (เต็มเก็บใน daemon log)
Reason: log มีแค่ 2 อาการใน channel นั้น: (1) เพิ่ม NousResearch ไม่ได้เพราะ Cloudflare ตอบ 403 HTML (4.5KB) ให้ Go UA — error ยาวจน followup เกิน 2000 ตัวอักษร ผู้ใช้จึงไม่เห็นสาเหตุ (เจอซ้ำ 4 ครั้ง หลายวัน); ยืนยันด้วย curl: Go UA=403, `ai`=200, opencode adapter UA ไม่กระทบเพราะ headers ของ adapter ถูก apply ทีหลัง; (2) `context canceled` 2 ครั้ง = turn ถูก preempt โดย turn ใหม่ของ session เดียวกัน (beginInterrupt — canceller เดียวใน process นอกจาก shutdown ซึ่งไม่เกิดเพราะ daemon รันต่อเนื่อง) เข้ากันได้กับ generation ช้า + ส่งข้อความซ้ำ, session DB ยัง 0 bytes เพราะไม่มี turn ใดจบ; ไม่พบโค้ดผิดสำหรับ cancel จึงแก้ที่สาเหตุทางอ้อม (provider ใช้ได้ + เห็น error จริง) ก่อน
Impact: sdk/providers/internal (http.go default UA), transport/discord (provider_settings.go truncate+log), tests ที่เกี่ยวข้อง
Validation: unit (default UA, explicit UA ชนะ, truncate ≤2000 + log เต็ม); live: Nous /models 200 ด้วย UA ใหม่ผ่าน curl; `go test ./... -timeout 3m`; `go vet ./...`; `git diff --check`
Status: accepted

CHANGE-028

Date: 2026-09-16
Type: revise
Request: ของ nousresearch ใช้คำว่า :free มันจะกรองได้มั้ย แถมบางเจ้าใช้ free/model_name อีก
Conflict: none (ขยาย matching เดิมที่ระบุแค่ -free ใน modal; ไม่มี REQ ล็อก suffix ไว้)
Previous: free_only เก็บเฉพาะ id ลงท้าย -free (opencode Zen)
New: free_only เก็บ id ที่ลงท้าย -free (Zen) หรือ :free (OpenRouter-style เช่น NousResearch: stepfun/step-3.7-flash:free) หรือขึ้นต้น free/ (เช่น Free/qwen-3); เทียบแบบ case-insensitive หลัง trim space
Reason: Nous มี free 7 รุ่นแต่ใช้ :free ต่อท้ายจึงถูกกรองทิ้งหมด; provider อื่นใช้ prefix free/
Impact: sdk/routing.go (isFreeModelID), discord provider modal description, sdk/routing_test.go
Validation: unit (4 รูปแบบผ่าน, freebie/gpt-5-free-tier/empty ถูกทิ้ง); `go test ./... -timeout 3m`; `go vet ./...`; `git diff --check`
Status: accepted

CHANGE-029

Date: 2026-09-16
Type: revise
Request: มันขึ้น retry แต่ไม่รู้ว่า retry ด้วยเหตุผลอะไร
Conflict: none (เติมเหตุผลในบรรทัดเดิม ไม่เปลี่ยน layout; คง REQ-022 ที่ห้าม raw result — เหตุผลผ่าน safeErrorSummary ที่ strip อยู่แล้ว)
Previous: actor trace (Components V2) แสดงแค่ "↻ retrying request [in Xs]" ไม่มีสาเหตุ (legacy path มีอยู่แล้ว)
New: บรรทัด retry ของ actor trace แสดงเหตุผลด้วย: "↻ retrying request (<safe summary>) [in Xs]" เช่น rate limited (HTTP 429), authorization failed (HTTP 401/403), service error (HTTP xxx)
Reason: ผู้ใช้เห็น retry แต่แยกไม่ออกว่า key ผิด / โดน rate limit / server ล่ม
Impact: transport/discord (actor_trace_display.go + test)
Validation: unit (reason ปรากฏ, timing ครบ); `go test ./... -timeout 3m`; `go vet ./...`; `git diff --check`
Status: accepted

CHANGE-030

Date: 2026-09-16
Type: revise
Request: ใช้ MS1.3 ใน opencode จริงได้ปกติ แต่ผ่านบอทได้ 500 (ยืนยันว่ารันในแอป opencode)
Conflict: none (ขยาย REQ-039; ไม่เปลี่ยน fingerprint/headers เดิม)
Previous: adapter opencode ยิง /chat/completions อย่างเดียว
New: REQ-039 แก้ — adapter opencode ยิง /responses ก่อน, fallback ไป /chat เมื่อ 404/400-model_not_supported/500; 401/403/429 return ทันที; Stream ด้วยหลักเดียวกัน (fallback ก่อนมี output)
Reason: ดัก traffic opencode ตัวจริงผ่าน logging proxy: builtin provider (OAuth) ยิง /responses + x-opencode-session/x-opencode-request/x-opencode-client/x-opencode-project แล้ว 200; ส่วน sk- key ยิง /chat กับ spark-1.3 ได้ 500 ไม่ว่า client ใด (รวม opencode เอง 4/4 ครั้ง) แต่ /responses + sk- key ได้ 200; กลับกัน mimo 500 บน /responses แต่ผ่านบน /chat — Zen แยกโมเดลตาม endpoint ทั้งสองทิศ จึงต้อง fallback ทั้ง Generate/Stream
Impact: sdk/providers/openai (export ResponsesResponse), sdk/providers/opencode (Generate/Stream fallback), tests
Validation: unit (responses ตรง, 404/500→chat, 429 ไม่ fallback, fingerprint ครบทั้งสองเส้น, stream fallback); live ผ่าน adapter จริงทั้ง spark + mimo; `go test ./... -timeout 3m`; `go vet ./...`; `git diff --check`
Status: accepted

CHANGE-031

Date: 2026-09-16
Type: revise
Request: sub agent โดน 400 (Invalid JSON schema: null is not of type "object" ที่ parameters) ทั้งที่ schema ถูกต้อง
Conflict: none (แก้ให้ส่งค่าถูกต้องตามที่ provider ต้องการ; ไม่เปลี่ยน tool semantics)
Previous: browserSchema(nil, nil) ของ list_attachments serialize เป็น "properties":null และ system prompt 78KB ถูกส่งในฟิลด์ instructions — Zen /responses ตอบ 400 ทั้งสองกรณี (ย้ำด้วย live: dev-input ผ่าน, dummy-size ผ่าน)
New: browserSchema แปลง properties=nil เป็น {} เสมอ (ครอบคลุม list_attachments และ browser_list_pages); adapter opencode ส่ง system prompt เป็น developer input item แรกแทนฟิลด์ instructions (ตรงกับ opencode ตัวจริงที่ดักได้)
Reason: null properties ทำให้ทุก turn ที่มี tools ของ worker/main พังทั้งหมดบน Zen; instructions ก้อนใหญ่ถูกปฏิเสธแยกอีกชั้น
Impact: tools/browser_tools.go, sdk/providers/opencode (buildResponsesRequest), tests ทั้งสองชั้น
Validation: unit (marshal ไม่มี null; body ไม่มี instructions + มี developer item แรก); live: list_attachments เดี่ยว 200, worker เต็ม (prompt 78KB + 13 tools) 200; `go test ./... -timeout 3m`; `go vet ./...`; `git diff --check`
Status: accepted

CHANGE-032

Date: 2026-09-16
Type: revise
Request: แค่ Hi ไปอย่างเดียวแต่มัน plan อะไรก็ไม่รู้ — ปรับ system prompt ให้เป็นระบบมากกว่านี้
Conflict: REQ-016 (main ต้อง delegate/plan ทุกงาน — เพิ่มข้อยกเว้นข้อความที่ไม่ใช่งาน)
Previous: Main Agent สร้างแผน/delegate แม้แต่คำทักทายที่ไม่มีงาน
New: REQ-016 เพิ่ม — ข้อความที่ไม่ใช้ tools/context/งาน (greeting/thanks/ack/คำถามตอบตรงได้) ให้ตอบตรงทันที ไม่สร้างแผน ไม่ delegate ไม่ investigate; planning/delegation เริ่มเมื่อมีงานจริงเท่านั้น (prompt ทั้ง defaultSystemPrompt และ planningSystemInstruction)
Reason: ทักทายแล้วโดน plan+delegate เปลือง, ช้า, และพังตามเมื่อ worker error — ไม่เป็นระบบ
Impact: cmd/ai/main.go, sdk/plan_tool.go, requirements/functional.md (REQ-016)
Validation: unit (prompt มี short-circuit rule ทั้งสองเส้น); `go test ./... -timeout 3m`; `go vet ./...`; `git diff --check`
Status: accepted

CHANGE-033

Date: 2026-09-16
Type: add
Request: Complete worker-produced Discord file send producer in SDK output path
Conflict: none (implements outbound leg of REQ-026; transport SendFiles path already exists)
Previous: `sdk/outbound_attachments.go` tracker existed with no producer or consumer wiring; no `send_attachment` worker tool; `HarnessLoop.Entry` never stamped outbound ids into `Output.Metadata`
New: Worker tool `send_attachment` (ref_id only) validates the opaque reference via session-scoped store Get, then records intent with `sdk.RecordOutboundAttachment` capped at `MaxOutboundAttachmentIDs` (10); `HarnessLoop.Entry` drains via `TakeOutboundAttachmentIDs` after the agent turn and stamps `Output.Metadata[out_attachment_ids]` (comma-joined, merged, per-turn dedup); confirmation text is short only, no bytes on the canonical text path
Reason: Live Discord turns must upload worker-requested files via the existing SendFiles path without leaking bytes/base64 through SDK text or planner context
Impact: tools/attachments.go (send handler), tools/registry.go (registration), sdk/loop.go (drain+stamp), tools/attachments_test.go (store interface conformance); no canonical Turn/ContentPart change
Validation: `go test ./sdk ./tools ./transport/discord -count=1`; `go vet ./sdk ./tools ./transport/discord`
Status: accepted

CHANGE-034

Date: 2026-09-16
Type: revise
Request: Implement hybrid milestone plus anomaly-gated reporting in sdk/subagent.go, no more fixed-interval progress spam
Conflict: REQ-019/021 (fixed progress every X tool calls, X default 5)
Previous: progress report ทุก X completed worker tool calls (X จาก config `sub_agent.report_every_tool_calls` ค่าเริ่มต้น 5) แบบ fixed modulo; progress report แสดง tools ทั้งหมดตั้งแต่ต้นพร้อม args ย่อ ไม่มี result excerpt
New: REQ-019 hybrid reporting — progress report ถูก gate ด้วย shouldReportProgress: ปล่อยเฉพาะ milestone tools (write_file/edit_file/bash) หรือ anomaly (error สองครั้งติด หรือครบ backstop interval นับจาก report ล่าสุด) และจำกัดไม่เกิน MaxMidJobReports (default 3) ครั้งต่อ job; defaultSubAgentReportInterval เปลี่ยน 5 → 20 เป็น safety backstop โดย ReportEveryToolCalls ยัง override ได้; progressLocked เป็น delta-only (เฉพาะ tools ใหม่นับจาก lastReportedToolCount) พร้อม 1-line status header (completed/new counts) และ result excerpt ตัดที่ 300 runes; heartbeat ผ่าน trace sink, one-job-per-parent, stop/follow_up และ final handoff ไม่เปลี่ยน
Reason: fixed-interval ทุก 5 calls ส่ง progress บ่อยเกิน เปลือง context ของ planner; milestone + anomaly จับจุดที่ planner ต้องตรวจ scope จริง (ไฟล์เปลี่ยน/คำสั่งรัน/ความล้มเหลวติดกัน) ส่วน backstop 20 กันงานเงียบยาวโดยไม่รายงาน
Impact: sdk/subagent.go (SubAgentConfig.MaxMidJobReports, job lastReportedToolCount/midJobReports, shouldReportProgress, delta progressLocked), sdk/session_workspace_test.go (default 20), requirements/functional.md REQ-019, requirements/changes.md
Validation: `go test ./sdk -count=1`; `go vet ./sdk`
Status: accepted

CHANGE-035

Date: 2026-09-16
Type: add
Request: Implement V2 heartbeat now using known APIs, no more searches
Conflict: none (lightweight status alongside detailed trace; final answer ping unchanged)
Previous: No per-actor lightweight status; long turns showed only detailed trace lines with no throttled liveness signal
New: REQ-041 — Discord per-actor V2 heartbeat status container (working / using tools with count only / retrying / done with total seconds); `heartbeatThrottleMs=3000` caps edits at 1 per 3s with ChannelTyping between edits and no per-second ticker; status send/edit use `MessageFlagsSuppressNotifications` with `MessageFlagsIsComponentsV2`; completion replaces the container once with a collapsed one-line receipt plus token footer
Reason: Long provider/tool turns need a quiet liveness signal without edit spam, pings, or extra tickers, while the detailed trace keeps full ordering/timing
Impact: transport/discord/actor_trace_display.go (heartbeat state, spinner helpers, throttle, typing, receipt, silent flags), requirements/functional.md (REQ-041), requirements/changes.md
Validation: `go test ./transport/discord -count=1` and `go vet ./transport/discord`
Status: accepted
