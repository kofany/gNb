
# Jak korzystać z bota poprzez SSH i DCC w prosty i przyjazny sposób

Bot został zaprojektowany tak, aby umożliwić Ci pełną kontrolę nad jego działaniem, nawet jeśli nie jesteś ekspertem technicznym. Możesz zarządzać botem za pomocą dwóch metod:

- **DCC (Direct Client-to-Client)**
- **SSH (Secure Shell)**

Poniżej wyjaśnię, czym są te metody i jak z nich korzystać w prosty sposób.

## 1. Korzystanie z bota poprzez DCC (Direct Client-to-Client)

### Co to jest DCC?

DCC to funkcja w protokole IRC, która pozwala na bezpośrednią komunikację między dwoma użytkownikami, omijając serwer. W naszym przypadku, pozwala to na bezpośrednie połączenie z botem i wydawanie mu poleceń w prywatnym oknie czatu.

### Dlaczego warto używać DCC z botem?

- **Bezpośrednia kontrola**: Masz bezpośredni dostęp do bota bez konieczności używania komend na kanale publicznym.
- **Prywatność**: Twoje polecenia i rozmowy z botem są widoczne tylko dla Ciebie.
- **Łatwość użycia**: Interfejs DCC jest prosty i przypomina zwykły czat.

### Jak nawiązać połączenie DCC z botem?

1. Znajdź bota na liście użytkowników w swoim kliencie IRC (np. mIRC, HexChat).
2. Kliknij prawym przyciskiem myszy na nick bota i wybierz opcję "Zaproszenie do DCC CHAT" lub podobną.
3. Wyślij prośbę o połączenie DCC CHAT. Bot automatycznie zaakceptuje Twoje zaproszenie i otworzy się nowe okno czatu.

### Jak korzystać z bota po nawiązaniu połączenia DCC?

- **Wydawanie poleceń**: Wszystkie polecenia zaczynają się od kropki `.`.
- **Przykładowe polecenia**:
  - `.help` - Wyświetla listę dostępnych poleceń.
  - `.msg #kanał Witajcie!` - Wysyła wiadomość "Witajcie!" na kanał #kanał.
  - `.join #nowy_kanał` - Bot dołącza do #nowy_kanał.
  - `.part #stary_kanał` - Bot opuszcza #stary_kanał.
  - `.nick NowyNick` - Bot zmienia swój pseudonim na NowyNick.
  - `.mode #kanał +m` - Ustawia tryb moderowany na #kanał.
  - `.kick #kanał Użytkownik Powód` - Usuwa Użytkownika z #kanał z powodem.

### Dodatkowe informacje

- **Bez prefiksu kropki**: Jeśli napiszesz wiadomość bez kropki na początku, bot potraktuje ją jako zwykłą wiadomość i wyśle do domyślnego odbiorcy (np. do siebie).
- **Pomoc**: Użyj polecenia `.help`, aby zobaczyć pełną listę komend i ich opisów.

## 2. Korzystanie z bota poprzez SSH (Secure Shell)

### Co to jest SSH?

SSH to protokół umożliwiający bezpieczne, zdalne połączenie z innym urządzeniem. W naszym przypadku, pozwala to na połączenie się z botem i zarządzanie nim w sposób podobny do korzystania z terminala (konsoli).

### Dlaczego warto używać SSH z botem?

- **Zaawansowana kontrola**: Masz dostęp do wszystkich funkcji bota w jednym miejscu.
- **Bezpieczeństwo**: Połączenie jest szyfrowane i bezpieczne.
- **Uniwersalność**: Możesz połączyć się z botem z dowolnego urządzenia z klientem SSH.

### Jak nawiązać połączenie SSH z botem?

#### Uruchomienie BNC (Bouncer)

Na kanale lub w prywatnej wiadomości do bota wpisz komendę:

```
!bnc start
```

Bot odpowie Ci, podając dane niezbędne do połączenia:
- **Adres (host)**: np. bot.mojaserwer.pl
- **Port**: np. 2222
- **Nazwa użytkownika**: zazwyczaj nick bota
- **Hasło**: unikalne hasło generowane przez bota

#### Połączenie się z botem za pomocą klienta SSH

Na komputerze:
- Jeśli korzystasz z systemu Linux lub macOS, otwórz terminal.
- Jeśli korzystasz z Windowsa, możesz użyć programu PuTTY lub wbudowanego w Windows 10/11 klienta SSH.

Wpisz komendę:

```
ssh -p [port] [użytkownik]@[adres]
```

**Przykład**:

```
ssh -p 2222 BotNick@bot.mojaserwer.pl
```

Wprowadź hasło, gdy zostaniesz o to poproszony.

### Korzystanie z bota po połączeniu

- Po pomyślnym zalogowaniu zobaczysz powitanie i instrukcje.
- **Wydawanie poleceń** odbywa się tak samo, jak w przypadku DCC:
  - **Polecenia zaczynają się od kropki `.`**.
  - **Przykłady**:
    - `.help` - Wyświetla listę dostępnych poleceń.
    - `.join #kanał` - Bot dołącza do kanału.
    - `.msg Użytkownik Cześć!` - Wysyła wiadomość do użytkownika.
- **Zakończenie sesji**: Aby się rozłączyć, możesz wpisać polecenie `.quit` lub po prostu zamknąć połączenie SSH.

#### Zatrzymanie BNC

Jeśli chcesz wyłączyć możliwość łączenia się z botem przez SSH, wpisz na kanale lub w prywatnej wiadomości do bota:

```
!bnc stop
```

Bot potwierdzi zatrzymanie BNC.

### Dodatkowe informacje

- **Bezpieczeństwo**: Upewnij się, że nie udostępniasz nikomu danych do logowania podanych przez bota.
- **Pomoc**: W każdej chwili możesz wpisać `.help`, aby zobaczyć dostępne komendy.

## Podsumowanie

Korzystanie z bota poprzez DCC i SSH daje Ci pełną kontrolę nad jego działaniem w prosty i intuicyjny sposób. Niezależnie od tego, czy wybierzesz DCC, czy SSH, masz dostęp do tych samych poleceń i funkcji.

### Dlaczego warto?

- **Prostota**: Nie musisz być ekspertem technicznym, aby zarządzać botem.
- **Elastyczność**: Wybierz metodę, która jest dla Ciebie wygodniejsza.
- **Pełna kontrola**: Zarządzaj botem, dodawaj i usuwaj nicki, kontroluj jego obecność na kanałach.

Jeśli masz pytania lub potrzebujesz pomocy:

- Na kanale lub w prywatnej wiadomości do bota: Zapytaj, a bot lub inni użytkownicy chętnie pomogą.
- W trakcie sesji DCC lub SSH: Użyj komendy `.help`, aby uzyskać więcej informacji.

Życzymy przyjemnego korzystania z bota!
