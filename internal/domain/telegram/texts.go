// Code in this file mirrors the "notif.challenge.*" texts of the web client.

package telegram

import "strings"

// texts are the bot's messages by language; {actor}, {n}, {user}, {site} and {url} are filled in.
var texts = map[string]map[string]string{
	"uz": {
		"day":          "{actor} chellenjining {n}-kunini yozdi",
		"milestone":    "{actor} chellenjida {n} kunga yetdi",
		"milestoneOwn": "Marra: chellenjingizda {n} kun bajarildi. Davom eting!",
		"other":        "Chellenjingizdan yangilik",
		"praise.0":     "Bugungi kun bajarildi. Zo‘r!",
		"praise.1":     "Yana bir kun qo‘lda. Sen uddalaysan!",
		"praise.2":     "Bugun ham yozdingiz. Mana shunday!",
		"praise.3":     "Bugungi hikoya hisobga o‘tdi. Barakalla!",
		"evening.0":    "Bugungi hikoya hali kutyapti. Sen uddalaysan!",
		"evening.1":    "Bir necha xatboshi — va bugun hisobda. Sen uddalaysan!",
		"evening.2":    "Bugunni o‘tkazib yubormang. Sen uddalaysan!",
		"evening.3":    "Kechqurun yozishga ayni vaqt. Sen uddalaysan!",
		"broken.0":     "Kecha o‘tib ketdi, bugun sizniki. Sen uddalaysan!",
		"broken.1":     "Bitta qoldirilgan kun — oxiri emas. Sen uddalaysan!",
		"broken.2":     "Bugun yangi seriya boshlang. Sen uddalaysan!",
		"broken.3":     "Bugun qayting va davom eting. Sen uddalaysan!",
		"silent.0":     "Bir necha kun bo‘ldi. Bitta qisqa hikoya sizni qaytaradi. Sen uddalaysan!",
		"silent.1":     "Chellenjingiz sizni sog‘indi. Sen uddalaysan!",
		"silent.2":     "Qayta boshlash eng qiyini. Sen uddalaysan!",
		"silent.3":     "Bugun kichik bir narsa yozing. Sen uddalaysan!",
		"linked":       "Telegram {site} saytidagi @{user} akkauntiga ulandi. Chellenj xabarlari shu yerga keladi.\n\nBu sizning akkauntingiz bo‘lmasa yoki xabarlarni to‘xtatmoqchi bo‘lsangiz, /stop yuboring.\n{url}",
		"invalid":      "Havola eskirgan yoki ishlatilgan. Saytdan yangi havola oling:\n{url}",
		"stopped":      "Telegram akkauntdan uzildi. Qayta ulash uchun saytga kiring:\n{url}",
		"help":         "Bu bot {site} chellenj xabarlarini yuboradi. Ulash uchun saytdagi “Telegram’ni ulash” tugmasini bosing. To‘xtatish: /stop\n{url}",
	},
	"en": {
		"day":          "{actor} wrote day {n} of their challenge",
		"milestone":    "{actor} reached {n} days in their challenge",
		"milestoneOwn": "Milestone: {n} days of your challenge done. Keep going!",
		"other":        "News from your challenge",
		"praise.0":     "Today is done. Great work!",
		"praise.1":     "One more day in the bag. You can do it all the way!",
		"praise.2":     "You showed up today. That is how it’s done.",
		"praise.3":     "Today’s story counts. Proud of you!",
		"evening.0":    "Today’s story is still waiting. You can do it!",
		"evening.1":    "A few paragraphs and today counts. You can do it!",
		"evening.2":    "Don’t let today slip by. You can do it!",
		"evening.3":    "Evening is a good time to write. You can do it!",
		"broken.0":     "Yesterday slipped, today is yours. You can do it!",
		"broken.1":     "One missed day is not the end. You can do it!",
		"broken.2":     "Start a new streak today. You can do it!",
		"broken.3":     "Come back today and keep going. You can do it!",
		"silent.0":     "It has been a few days. One short story brings you back. You can do it!",
		"silent.1":     "Your challenge misses you. You can do it!",
		"silent.2":     "Restarting is the hardest part. You can do it!",
		"silent.3":     "Write something small today. You can do it!",
		"linked":       "Telegram is now connected to the @{user} account on {site}. Challenge messages will arrive here.\n\nIf that is not your account, or to stop the messages, send /stop.\n{url}",
		"invalid":      "This link has expired or was already used. Get a new one on the site:\n{url}",
		"stopped":      "Telegram is disconnected from your account. To connect again, visit the site:\n{url}",
		"help":         "This bot sends {site} challenge messages. To connect, press “Connect Telegram” on the site. To stop: /stop\n{url}",
	},
	"ru": {
		"day":          "{actor}: написан день {n} челленджа",
		"milestone":    "{actor}: пройдено дней в челлендже — {n}",
		"milestoneOwn": "Рубеж: дней в вашем челлендже — {n}. Так держать!",
		"other":        "Новости вашего челленджа",
		"praise.0":     "Сегодняшний день засчитан. Отлично!",
		"praise.1":     "Ещё один день в копилке. У вас получится дойти до конца!",
		"praise.2":     "Вы сегодня написали. Так и надо.",
		"praise.3":     "Сегодняшняя история засчитана. Здорово!",
		"evening.0":    "Сегодняшняя история ещё ждёт. У вас получится!",
		"evening.1":    "Пара абзацев — и день засчитан. У вас получится!",
		"evening.2":    "Не упустите сегодняшний день. У вас получится!",
		"evening.3":    "Вечер — хорошее время писать. У вас получится!",
		"broken.0":     "Вчера пропущено, но сегодня ваше. У вас получится!",
		"broken.1":     "Один пропуск — не конец. У вас получится!",
		"broken.2":     "Начните новую серию сегодня. У вас получится!",
		"broken.3":     "Возвращайтесь и продолжайте. У вас получится!",
		"silent.0":     "Прошло несколько дней. Одна короткая история вернёт вас. У вас получится!",
		"silent.1":     "Челлендж по вам скучает. У вас получится!",
		"silent.2":     "Начать снова труднее всего. У вас получится!",
		"silent.3":     "Напишите сегодня что-нибудь небольшое. У вас получится!",
		"linked":       "Telegram подключён к аккаунту @{user} на сайте {site}. Сюда будут приходить сообщения о челленджах.\n\nЕсли это не ваш аккаунт или вы хотите отключить сообщения, отправьте /stop.\n{url}",
		"invalid":      "Ссылка устарела или уже использована. Получите новую на сайте:\n{url}",
		"stopped":      "Telegram отключён от аккаунта. Чтобы подключить снова, зайдите на сайт:\n{url}",
		"help":         "Этот бот присылает сообщения о челленджах {site}. Чтобы подключить его, нажмите «Подключить Telegram» на сайте. Отключить: /stop\n{url}",
	},
}

// Text returns the message for key in lang with {name} placeholders replaced
// by the pairs in args; "" for an unknown key.
func Text(lang, key string, args ...string) string {
	pairs := make([]string, 0, len(args))
	for i := 0; i+1 < len(args); i += 2 {
		pairs = append(pairs, "{"+args[i]+"}", args[i+1])
	}
	return strings.NewReplacer(pairs...).Replace(texts[Lang(lang)][key])
}
