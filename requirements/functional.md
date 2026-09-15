# ข้อกำหนดด้านการทำงาน

REQ-001 — Harness รับ canonical input ได้โดยไม่ขึ้นกับ transport

REQ-002 — CLI และ Discord สามารถใช้ Harness/Agent runtime เดียวกันได้

REQ-003 — แต่ละ session มี state แบบถาวรที่แยกจากกัน และสามารถกู้คืนได้หลัง process restart

REQ-004 — Agent สามารถทำลูป model → tool call → tool result → model จนกว่าจะไม่มี tool call เหลือ

REQ-005 — ความล้มเหลวของ tool ต้องถูกส่งกลับมาในรูปแบบ tool result เพื่อให้ Agent สามารถกู้คืนได้โดยไม่ทำให้ทั้ง turn ล้มเหลว

REQ-006 — รูปแบบ request/response เฉพาะของ provider ต้องถูกแยกไว้หลัง adapter และแปลงผ่าน canonical SDK model

REQ-007 — การค้นหา provider model ต้องใช้ catalogue ของ provider ที่ดึงจากระบบจริง และเมื่อ refresh สำเร็จต้องแทนที่ข้อมูล catalogue เดิมที่ค้างอยู่

REQ-008 — การ retry ต้องใช้ API key ของ session ที่เลือกไว้ และห้ามเปลี่ยน key โดยอัตโนมัติ

REQ-009 — output แบบ streaming จะต้องไม่ถูก replay โดยอัตโนมัติหลังจากเริ่มส่ง output แล้ว

REQ-010 — Browser automation ต้องใช้งานได้ผ่าน Go CDP implementation ที่อยู่ในตัว โดยไม่ใช้ Playwright หรือ Node.js worker

REQ-011 — Runtime configuration ต้องเป็นแบบไฟล์ภายใต้ layout `~/.local/share/ai` ที่กำหนดไว้ และไม่ต้องใช้ environment variable ในการตั้งค่า

REQ-012 — Project requirements ต้องเก็บอยู่ใน repository และถือเป็น source of truth สำหรับงานของโปรเจค

REQ-013 — ก่อน implement request ใหม่ AI ต้องตรวจสอบ repository requirements และบันทึก specification change ที่ request นั้นทำให้เกิดขึ้น

REQ-014 — เมื่อสร้าง software project ใหม่ AI ต้องสร้างและเติมข้อมูลใน `requirements/` ของ project นั้นก่อนเริ่ม implementation ในส่วนสำคัญ

REQ-015 — การเปลี่ยนแปลงโค้ดต้องรักษาความรับผิดชอบของแต่ละ module ให้ชัดเจน และต้องเพิ่มหรือแก้ functionality ใน module ที่รับผิดชอบโดยตรง เว้นแต่มีการเปลี่ยน architecture ที่บันทึกไว้อย่างชัดเจน

REQ-016 — เมื่อเปิด planning, Main Agent จะได้รับเฉพาะ planning tools, tools สำหรับ orchestration ของ sub-agent และ tool สำหรับส่งต่อ opaque file reference (ชื่อ/ชนิด/ขนาด/relative path ภายใน file store) เท่านั้น ไม่ใช่เนื้อหาไฟล์ และต้องปฏิเสธ execution call โดยตรง รวมถึงหลังจากบันทึกแผนแล้วด้วย Main Agent ห้ามได้รับ byte stream, base64, MIME data หรือเนื้อหาภายในไฟล์ผ่านช่องทางใด ๆ รวมถึงผ่าน tool result ของ orchestration ด้วย file reference ต้องถูก resolve โดย worker agent หรือ transport module เท่านั้น ค่าเริ่มต้นของคำสั่ง Main Agent ต้องมอบหมายการตรวจสอบโปรเจคและการดำเนินงาน แทนการสั่งให้ Main Agent ใช้ worker tools โดยตรง โดยความพยายามต้องได้สัดส่วนกับความซับซ้อนของงาน: งานที่ต้องใช้ repository context เท่านั้นจึงต้องมีขั้นตอน investigation แยกต่างหาก งานเล็กน้อยที่ไม่ต้องใช้ repository context (เช่น สร้าง/ลบไฟล์เดียว) ต้องใช้แผนขั้นเดียวที่สั้นที่สุดโดยข้าม investigation แยก และแผนทุกขนาดต้องมีจำนวน step น้อยที่สุดที่ครอบคลุมเป้าหมาย

REQ-017 — เมื่อ `DisablePlanning: true`, execution/worker agent ต้องคง system prompt และ execution tool definitions ที่ได้รับมาไว้ทั้งใน turn ปกติและ streaming turn รวมถึงตอนทำต่อหลัง tool result; Harness ห้ามแทรกข้อจำกัดของ Main Agent หรือ planning/delegation tools เข้าไป ค่าเริ่มต้นของคำสั่ง sub-agent ต้องจำกัดงานให้อยู่ใน scope ที่ได้รับ ห้าม delegation ต่อ และห้ามสื่อสารกับ end user โดยตรง และต้องรายงานสิ่งที่ตรวจพบ/ผลลัพธ์ให้ planner การตรวจสอบผลต้องใช้วิธีที่น้อยที่สุดแต่เพียงพอ: คำสั่งเดียวที่พิสูจน์ผลลัพธ์ได้ (หรือ shell line เดียวที่รวมการตรวจที่เกี่ยวข้อง) ห้ามทำซ้ำรายการที่เทียบเท่ากัน (เช่น ls/wc/cat/stat ไฟล์เดียวกัน) เมื่อพิสูจน์ผลได้แล้ว และห้ามลองสูตรคำสั่งแบบอื่นต่อหลังจากการตรวจสำเร็จแล้ว

REQ-018 — การประกอบ role prompt ต้องรักษา repository requirements และ custom context ไว้ ไม่ตัด section หรือบรรทัดที่ไม่เกี่ยวข้องออกด้วยการจับคู่ชื่อ tool แบบ heuristic ขอบเขต role ของ Main Agent ต้องมีผลเหนือคำสั่ง direct-execution ที่ขัดแย้งกันใน context ที่ได้รับมา และค่าเริ่มต้นของ CLI/SDK ที่สร้างในตัวต้องไม่ขัดแย้งกับ role ที่ได้รับมอบหมาย

REQ-019 — เมื่อ worker loop จบลง ต้องสร้างเพียงผลลัพธ์เพื่อให้ Main Agent ตรวจสอบ และห้ามรับหรือเลื่อนแผนต่อโดยอัตโนมัติ Main Agent ต้องอ่านข้อความสรุป, ประวัติการใช้ tool (ชื่อ tool, arguments/รายละเอียด, ผลลัพธ์รวม error flag) และหลักฐานการตรวจสอบที่ orchestration จัดให้ ตรวจสอบความสำเร็จ และยอมรับ step ที่บันทึกไว้อย่างชัดเจนผ่าน orchestration การ review ของ Main Agent จำกัดอยู่ที่ข้อความสรุป, ประวัติ tool, validation evidence และสถานะ เท่านั้น และไม่นับ raw file content หรือ large binary payload ผลลัพธ์ที่ worker สร้างไฟล์ซึ่ง transport ต้องจัดเก็บให้ส่งต่อเป็น file reference พร้อมชื่อ/ชนิด/ขนาดเท่านั้น ผลลัพธ์ที่ล้มเหลว หยุด หรือยังไม่สมบูรณ์ต้องสามารถ retry ต่อใน worker session เดิมได้ และงานใหม่ที่ได้รับมอบหมายต้องสามารถสั่งต่อเข้า worker session เดิมได้โดยคงประวัติ session เดิมไว้ Planner guidance ต้องรอ lifecycle completion event แทนการ polling ซ้ำ ๆ และยังต้องมี explicit status request ให้ใช้ได้

REQ-020 — การ delegation ต้องจอง running job ได้พร้อมกันไม่เกินหนึ่งรายการต่อ parent session โดยครอบคลุม investigation, planned work, follow-up และ continue Job ต้องเก็บ plan revision และ step identity การ completion, retry, continue และ acceptance ต้องตรวจสอบ transition และห้ามเปลี่ยนแผนใหม่ที่เข้ามาแทนที่ History, status, stop, follow-up, continue และ acceptance ต้องตรวจสอบ ownership ของ parent History ของแต่ละ job ต้องแสดงรายการ tool ที่ worker ใช้พร้อมชื่อ, arguments/รายละเอียด และผลลัพธ์ (รวมสถานะ error) ของแต่ละ tool นอกเหนือจากข้อความสรุปและผลลัพธ์สุดท้าย Continue ต้อง reuse worker session เดิม (workerID/session database เดิม) สำหรับงานใหม่ได้แม้ job ก่อนหน้าจะถูก accept แล้ว โดยต้องไม่มี running job ซ้อนกันและต้องผูกกับ plan step ปัจจุบันหรือเป็น investigation เมื่อแผนเสร็จแล้วต้องยังสามารถทำ investigation หรืองานต่อเนื่องสำหรับงานใหม่ได้โดยไม่ล้างหรือข้ามงานที่กำลังทำอยู่

REQ-021 — Lifecycle continuation ต้องรักษา canonical source ของ input ที่เริ่มต้น, session routing identity และ metadata เดิมทั้งหมด (รวม `channel_id` ของ Discord) โดยไม่ขึ้นกับรูปแบบการเขียน session ID หาก continuation ล้มเหลวต้องใช้เส้นทางรายงาน turn-error เดิม พฤติกรรม cancellation และขอบเขตการ persist session เดิมต้องไม่เปลี่ยน และ reservation/revision ของ orchestration เป็นแบบ process-local

REQ-022 — Discord ต้องแสดง canonical response text เป็น Markdown ที่อ่านง่าย ไม่ใช่ SDK output ที่ serialize เป็นข้อมูลดิบ Progress ต้องกระชับ และไม่แสดง raw tool arguments/results, delegated task text, reasoning text หรือ internal planning/review chatter แต่ยังคงแสดง operation label ที่ปลอดภัย, ตัวบ่งชี้ความล้มเหลว, เวลา retry, usage และเวลาที่ใช้ได้อย่างมีประโยชน์ การ render ของ transport ต้องอยู่ใน Discord module

REQ-023 — Discord final และ streamed response text ต้องแบ่งหน้าแบบ lossless ที่ขอบเขต Unicode code point ภายในข้อจำกัด message/embed โดยนับ supplementary characters เป็น UTF-16 units อย่างระมัดระวัง ต้องคง whitespace ที่ขอบเขตไว้ และเมื่อมี fenced code block ต้องปิด/เปิด fence สำหรับการแสดงแต่ละหน้าโดยไม่ลบ content ต้นฉบับ Streaming update ต้องแก้ไขหน้าที่มีอยู่ และ terminal response trace ต้องไม่ replay content ที่ถูก stream ไปแล้ว Progress สามารถเป็น summary ที่มีขอบเขตได้ แต่ response text ต้องไม่ถูกตัดทอน

REQ-024 — Discord text/progress transition ต้องส่งต่อ flush failure และเก็บ buffered state ที่ยังไม่ได้ส่งไว้เพื่อ retry แทนการ reset เมื่อส่ง page สำเร็จต้องบันทึกไว้เพื่อไม่ให้ retry ส่งซ้ำ Success/error terminal path ต้องหยุด live footer update, พยายาม flush buffer ที่ค้าง และล้าง retry-status แม้ operation อื่นจะล้มเหลว และต้องแสดง display error ให้ผู้ใช้เห็น โดยยังคงใช้ trace throttling/cooldown เดิม และ reset/close ต้องยกเลิก timer ที่รออยู่ คำสั่ง interaction ที่ต้องทำงานที่ใช้เวลา (สร้าง channel, โหลด model catalogue, บันทึก settings, อ่านรายการ session) ต้อง defer interaction response แบบ ephemeral ก่อนเริ่มงาน แล้วส่งผลลัพธ์หรือข้อผิดพลาดทาง followup เพื่อไม่ให้ interaction หมดอายุภายใน 3 วินาที ข้อยกเว้นคือการเปิด modal ซึ่งต้องตอบทันทีเพราะ Discord ไม่อนุญาตให้เปิด modal ต่อจาก deferred response

REQ-025 — Transport ที่รับไฟล์ (Discord attachment) ต้องดาวน์โหลดไฟล์นั้นแล้วแปลงเป็น file reference ใน file store และแนบ reference ผ่าน `Input.Metadata` โดยไม่เปลี่ยน canonical Turn/ContentPart contract และต้องคง session routing identity กับ metadata เดิมทั้งหมดไว้ รวมถึง `channel_id` ของต้นทาง ข้อความที่มีเฉพาะ attachment ต้องไม่กลายเป็น turn ว่าง

REQ-026 — Worker agent ต้องมี tool สำหรับอ่านไฟล์จาก file store ภายใน root ที่จำกัดด้วย safePath/withinRoot discipline ของ module tool นั้น แล้วสรุปเนื้อหาและสถานะกลับให้ planner ส่วนการส่งไฟล์ออกแบบจริงต้องอยู่ใน transport module เท่านั้น และห้ามส่ง raw bytes หรือ base64 ผ่าน canonical SDK text path

REQ-027 — Discord ต้องมีคำสั่ง `/new` สำหรับสร้าง text channel ใหม่ใน guild เดียวกับช่องที่เรียกคำสั่ง โดยตั้งชื่อช่องจากวันที่และเวลา (รูปแบบ `ai-YYYY-MM-DD-HHMM`) และคัดลอก model settings ของ session ต้นทาง (provider, model, temperature, thinking level, API key pool index) ไปยัง session ของช่องใหม่ทั้งหมด เมื่อสร้างสำเร็จต้องส่งข้อความสรุปการตั้งค่า (provider/model/thinking/temperature/API pool) เข้าไปในช่องใหม่ กรณีเรียกนอก guild, session ต้นทางยังไม่มี model settings หรือสร้างช่องไม่สำเร็จ ต้องตอบกลับแบบ ephemeral ในช่องเดิมโดยไม่สร้าง session ใหม่ พฤติกรรมทั้งหมดอยู่ใน Discord module และห้ามเปลี่ยน core orchestration, persistence หรือ canonical contract

REQ-028 — Discord คำสั่ง `/model` ต้องเปิด control panel ในข้อความปกติข้อความเดียว (regular message ไม่ใช่ ephemeral เพราะแก้ไขไม่ได้) แล้วแก้ไขข้อความเดิมทุกครั้งที่มีการกด โดยทุก control บันทึกผลทันที ไม่มี staging ไม่มี modal หนึ่งข้อความมีได้ไม่เกิน 5 action rows ประกอบด้วยแถวปุ่ม `[Main agent] [Sub agent]`, select menu provider (ไม่ preselect), select menu model (ไม่ preselect), แถวปุ่ม `[Thinking: ...] [Temp: ...]` ที่กดแล้วเปิด modal ให้เลือก thinking จาก select menu และกรอก temperature เป็นตัวอักษร (default หรือ 0.0–2.0) เมื่อ submit ต้องบันทึกแล้วแก้ไข panel ข้อความเดิม และ select menu API pool เมื่อ provider หรือ model catalogue มีรายการเกิน 25 ให้แบ่งหน้าในตัวเลือกเองโดยตัวเลือกที่ 1–2 คือ `Previous`/`Next` เสมอ และชื่อเมนูต้องบอกหน้าปัจจุบัน (เช่น `Select model (1/2)`) การเปลี่ยน provider ต้องคง model เดิมไว้ถ้ายังอยู่ใน catalogue ไม่เช่นนั้นใช้ model แรกของ catalogue เพื่อให้ session ใช้งานได้เสมอ

REQ-029 — Session ต้องจำ agent mode (`main` หรือ `sub`, ค่าเริ่มต้น `main`) แบบ persist โหมด `main` ทำงานแบบ planner เดิม (planning prompt + เครื่องมือ orchestration ไม่มี execution tools) โหมด `sub` ทำงานแบบ worker ตรง (execution tools เต็ม ไม่มี planning wrapper) โดยไม่ต้องพึ่ง global `DisablePlanning` switch การสลับโหมดต้องมีผลกับ turn ถัดไปของ session นั้นทันที
