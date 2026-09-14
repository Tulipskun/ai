# ข้อกำหนดผลิตภัณฑ์

## วัตถุประสงค์

`ai` คือ AI Harness ที่พัฒนาด้วย Go ซึ่งให้ Agent runtime ที่ไม่ผูกกับ provider รายใดรายหนึ่ง พร้อม transport สำหรับ CLI และ Discord, session แบบถาวร, tools, browser automation และการ routing ไปยัง provider

## เป้าหมาย

- ทำให้แกนกลางของ Harness ไม่ขึ้นกับ transport รายใดรายหนึ่ง
- ให้ Agent สามารถประมวลผล tool call และทำงานต่อจากผลลัพธ์ของ tool ได้
- ซ่อนรูปแบบ wire format เฉพาะของแต่ละ provider ไว้หลัง adapter และแปลงผ่าน request/response model กลาง
- รักษาสถานะของ session เพื่อให้สามารถทำงานต่อได้อย่างปลอดภัยเมื่อเปลี่ยน turn หรือ process ถูก restart
- ทำให้พฤติกรรมของโปรเจคมีข้อกำหนดที่ชัดเจนและดูแลรักษาได้เมื่อ requirements เปลี่ยนแปลง

## สิ่งที่ไม่ใช่เป้าหมาย

- ใช้ memory หรือ chat history ของ AI เป็น specification หลักของโปรเจค
- เก็บ project requirements ไว้เฉพาะใน system prompt
- ผูก core Agent เข้ากับ Discord, CLI หรือ provider รายใดรายหนึ่ง
