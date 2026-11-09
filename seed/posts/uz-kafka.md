Kafka oddiy navbat emas, u yozuvlar tartib bilan saqlanadigan taqsimlangan log. Bu farqni tushunish ko'p xatolarning oldini oladi.

## Asosiy tushunchalar

- **Topic** bir turdagi xabarlar oqimi.
- **Partition** topic'ning tartiblangan bo'lagi. Tartib faqat bitta partition ichida kafolatlanadi.
- **Consumer group** bitta partition'ni guruhdagi faqat bitta consumer o'qiydi.

Xabar o'qilgandan keyin o'chmaydi. Consumer faqat o'z offset'ini saqlaydi, shuning uchun xatoni tuzatgach ma'lumotni qaytadan o'qish mumkin.

## Partition kalitini tanlash

Bitta buyurtmaga oid hodisalar tartibi muhim bo'lsa, `order_id` ni kalit qiling. Kalit noto'g'ri tanlansa, masalan bitta yirik mijozning `tenant_id` si, bitta partition haddan tashqari yuklanadi.

## Ishonchli yozish

```properties
acks=all
enable.idempotence=true
min.insync.replicas=2
```

Replication factor 3 bilan birga bu sozlamalar bitta broker ishdan chiqqanda xabar yo'qolishining oldini oladi.

## Consumer lag

Eng muhim metrika: consumer yozilgan xabarlardan qancha orqada qolgani. Lag o'sib borayotgan bo'lsa, consumer sonini oshiring, lekin ular partition sonidan ko'p bo'lsa, ortiqchalari bo'sh turadi. Shuning uchun partition sonini oldindan biroz ko'proq rejalashtiring.
