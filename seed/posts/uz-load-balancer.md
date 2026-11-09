Load balancer so'rovlarni bir nechta server orasida taqsimlaydi va ishdan chiqqan serverni trafikdan chiqaradi. Asosiy savol: u qaysi darajada ishlaydi?

## L4 va L7

**L4** TCP darajasida ishlaydi va so'rov mazmunini ko'rmaydi. Juda tez va arzon. **L7** HTTP'ni tushunadi: yo'l, header va cookie bo'yicha yo'naltira oladi, TLS'ni shu yerda tugatadi. Ko'p tizimda ikkalasi birga ishlaydi: oldida L4, orqasida L7.

## Algoritmlar

- **Round robin** serverlar bir xil va so'rovlar qisqa bo'lganda yetarli.
- **Least connections** so'rov davomiyligi har xil bo'lganda yaxshiroq.
- **Consistent hashing** bir mijozni bir serverga yo'naltiradi, kesh serverlari uchun qulay.

## Health check

Health check faqat "jarayon tirikmi" deb so'ramasin. Lekin bazaga bog'liq tekshiruv ham xavfli: baza sekinlashsa, barcha serverlar birdaniga trafikdan chiqadi va kichik muammo to'liq uzilishga aylanadi. Readiness tekshiruvini sodda saqlang.

```nginx
upstream api {
    least_conn;
    server 10.0.1.10:8080 max_fails=3 fail_timeout=10s;
    server 10.0.1.11:8080 max_fails=3 fail_timeout=10s;
}
```

## Load balancer'ning o'zi-chi?

U ham bitta nosozlik nuqtasi bo'lishi mumkin. Odatda ikkita instance va virtual IP yoki bulutdagi boshqariladigan xizmat ishlatiladi.
