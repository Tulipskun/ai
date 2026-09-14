# การตัดสินใจด้านสถาปัตยกรรม

DEC-001 — Requirement ต้องอยู่ใน repository ของโปรเจค ไม่ใช่ใน AI memory

เหตุผล: specification ต้องพร้อมใช้งานสำหรับ agent, contributor, branch และ clone ของโปรเจคในอนาคตทุกตัว

DEC-002 — Project requirements แยกเป็นเป้าหมายผลิตภัณฑ์, ข้อกำหนดด้านการทำงาน, ข้อจำกัด, การตัดสินใจ และประวัติการเปลี่ยนแปลง

เหตุผล: แยกเจตนาที่คงที่ออกจาก implementation constraints และประวัติ เพื่อให้ agent สามารถวิเคราะห์ผลกระทบได้โดยไม่ต้องเขียน specification ทั้งหมดใหม่

DEC-003 — หาก user request ขัดแย้งกับ requirement เดิม ต้องจัดการเป็น requirement change ก่อน implementation

เหตุผล: การ implement พฤติกรรมที่ขัดแย้งโดยไม่บันทึกจะทำให้ specification ของโปรเจคกับโค้ดเกิด drift โดยไม่มีเอกสารกำกับ

DEC-004 — Software project ใหม่ที่ AI สร้างต้องมี `requirements/` อยู่ใน repository ของโปรเจคก่อนเริ่ม implementation ในส่วนสำคัญ

เหตุผล: repository ของโปรเจคต้องมี specification ของตัวเองตั้งแต่เริ่ม implementation
