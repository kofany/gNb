
# Tryb Canvas Otwarty

Już otwieram tryb canvas. Możesz teraz kontynuować swoje pytania lub zlecenia w tej przestrzeni.

## Opis Bota Nick Catcher

Ten bot został stworzony, aby automatycznie monitorować i przejmować wolne pseudonimy (nicki) na platformie IRC (Internet Relay Chat). Dzięki niemu możesz mieć pewność, że ważne dla Ciebie nicki będą zawsze pod Twoją kontrolą.

### Jak to działa?

Bot działa w tle i stale sprawdza dostępność wybranych przez Ciebie nicków. Gdy tylko któryś z nich stanie się wolny (np. gdy inny użytkownik się wyloguje lub zmieni nick), bot automatycznie przejmuje ten nick. W ten sposób nie musisz ciągle monitorować sytuacji – bot zrobi to za Ciebie.

### Kontrola nad botem

Możesz zarządzać botem na kilka sposobów:

#### Poprzez komendy na kanale lub w wiadomości prywatnej:

- **Jak to działa?** Wystarczy wysłać wiadomość z odpowiednią komendą na kanale, na którym jest bot, lub bezpośrednio do niego w wiadomości prywatnej.
- **Co możesz zrobić?** Możesz wydawać polecenia takie jak dołączanie do kanału, opuszczanie kanału, wyłączenie bota czy zarządzanie listą nicków i właścicieli.

#### Poprzez DCC (Direct Client-to-Client):

- **Co to jest DCC?** To funkcja w IRC, która umożliwia bezpośrednią komunikację z botem.
- **Jak to działa?** Możesz nawiązać z botem sesję DCC CHAT, w której masz dostęp do zaawansowanych poleceń i pełnej kontroli.

#### Poprzez SSH:

- **Co to jest SSH?** To bezpieczny sposób zdalnego połączenia z botem, dający dostęp do interfejsu tekstowego.
- **Jak to działa?** Bot posiada funkcję BNC (Bouncer), która pozwala na połączenie przez SSH. Możesz uruchomić BNC komendą `!bnc start`, a następnie połączyć się z botem za pomocą dostarczonych danych.

## Dostępne komendy na kanale lub w wiadomości prywatnej

### Dołączanie do kanału

- **Polecenie:** `!join <kanał>`
- **Opis:** Bot dołącza do wskazanego kanału.
- **Przykład:** `!join #nowy_kanał`

### Opuszczanie kanału

- **Polecenie:** `!part <kanał>`
- **Opis:** Bot opuszcza wskazany kanał.
- **Przykład:** `!part #stary_kanał`

### Wyłączenie bota

- **Polecenie:** `!quit`
- **Opis:** Bot rozłącza się z serwerem IRC.
- **Przykład:** `!quit`

### Ponowne połączenie bota

- **Polecenie:** `!reconnect`
- **Opis:** Bot rozłącza się i ponownie łączy z serwerem, co może pomóc w przypadku problemów z połączeniem.

### Wysyłanie wiadomości jako bot

- **Polecenie:** `!say <kanał/nick> <wiadomość>`
- **Opis:** Bot wysyła wiadomość na wskazany kanał lub do użytkownika.
- **Przykład:** `!say #kanał Witajcie!`

### Dodawanie nicków do monitorowania

- **Polecenie:** `!addnick <nick>`
- **Opis:** Dodaje nowy nick do listy monitorowanych przez bota.
- **Przykład:** `!addnick SuperNick`

### Usuwanie nicków z monitorowania

- **Polecenie:** `!delnick <nick>`
- **Opis:** Usuwa nick z listy monitorowanych.
- **Przykład:** `!delnick StaryNick`

### Wyświetlanie listy monitorowanych nicków

- **Polecenie:** `!listnicks`
- **Opis:** Wyświetla wszystkie nicki, które bot aktualnie monitoruje.

### Dodawanie właścicieli (ownerów)

- **Polecenie:** `!addowner <mask>`
- **Opis:** Dodaje nowego właściciela bota. Właściciele mają uprawnienia do zarządzania botem.
- **Przykład:** `!addowner *!*@adres.ip`

### Usuwanie właścicieli

- **Polecenie:** `!delowner <mask>`
- **Opis:** Usuwa właściciela z listy.
- **Przykład:** `!delowner *!*@adres.ip`

### Wyświetlanie listy właścicieli

- **Polecenie:** `!listowners`
- **Opis:** Wyświetla wszystkich aktualnych właścicieli bota.

## Zarządzanie BNC (SSH)

### Uruchomienie BNC

- **Polecenie:** `!bnc start`
- **Opis:** Bot uruchamia funkcję BNC, dzięki której możesz połączyć się z nim przez SSH.
- **Przykład:** `!bnc start`

### Zatrzymanie BNC

- **Polecenie:** `!bnc stop`
- **Opis:** Bot zatrzymuje funkcję BNC.
- **Przykład:** `!bnc stop`

