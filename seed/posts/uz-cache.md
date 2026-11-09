Kesh tizimni tezlashtiradi, lekin noto'g'ri ishlatilsa eski ma'lumot ko'rsatadi yoki bazani qulatadi. Eng ko'p ishlatiladigan naqshlarni ko'rib chiqamiz.

## Cache-aside

Eng keng tarqalgan naqsh. Dastur avval keshdan o'qiydi, topilmasa bazadan olib keshga yozadi.

```python
def get_user(user_id):
    cached = redis.get(f"user:{user_id}")
    if cached:
        return json.loads(cached)
    user = db.fetch_user(user_id)
    redis.setex(f"user:{user_id}", 300, json.dumps(user))
    return user
```

Ma'lumot o'zgarganda kalitni yangilamang, **o'chiring**. Ikki parallel yozuv keshda eski qiymat qoldirishi mumkin, o'chirish esa bu xavfni kamaytiradi.

## TTL nima uchun kerak

O'chirish buyrug'i tarmoq xatosi sababli yetib bormasligi mumkin. TTL esa eski ma'lumotning yashash muddatiga chegara qo'yadi. TTL'ga tasodifiy qiymat qo'shing, aks holda minglab kalitlar bir vaqtda eskiradi.

## Thundering herd

Mashhur kalit eskirganda yuzlab so'rovlar bir vaqtda bazaga boradi. Yechimlar:

- Bitta so'rov qiymatni qayta hisoblaydi, qolganlari kutadi (`SET NX` bilan lock).
- Kalit eskirishidan oldin fon jarayoni uni yangilaydi.
- Qisqa vaqt eski qiymat qaytariladi, yangisi fonda hisoblanadi.

## Nimani keshlamaslik kerak

Pul qoldig'i kabi har doim aniq bo'lishi shart bo'lgan ma'lumotni keshdan o'qib qaror qabul qilmang. Avval o'lchang: kesh hit rate 80% dan past bo'lsa, u foydadan ko'ra murakkablik keltirayotgan bo'lishi mumkin.
