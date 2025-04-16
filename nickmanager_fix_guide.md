# Problem niespójności nicków w systemie gNb

## Opis problemu

System gNb przestaje prawidłowo łapać nicki po około 12 godzinach działania. Analiza logów wskazuje na dwa powiązane problemy:

1. **Nieudane próby zmiany nicków**: Boty próbują zmienić nicki, ale operacje kończą się niepowodzeniem:
   ```
   WARNING: Failed to change nick for bot yl9261012 from yl9261012 to v
   WARNING: Failed to change nick for bot ritsu from ritsu to y
   ```

2. **Błędy ISON po kilku godzinach**: System zaczyna zgłaszać błędy związane z ISON dla nicków, których boty w rzeczywistości nie mają:
   ```
   WARNING: Bot j did not receive ISON response in time for request j-1744758796083588764
   ERROR: Error requesting ISON from bot j: bot j did not receive ISON response in time
   ```

Główna przyczyna: **NickManager uważa, że boty mają nicki, których w rzeczywistości nie udało się im uzyskać**.

## Kluczowe miejsca w kodzie

### 1. NickManager: wysyłanie żądań zmiany nicków bez weryfikacji powodzenia

W `nickmanager.go`:
```go
func (nm *NickManager) handleISONResponse(onlineNicks []string) {
    // ...
    for assignedBots < len(availableBots) && len(availablePriorityNicks) > 0 {
        nick := availablePriorityNicks[0]
        availablePriorityNicks = availablePriorityNicks[1:]
        bot := availableBots[assignedBots]
        
        // Skip single-letter nicks for servers that don't accept them
        if len(nick) == 1 && nm.NoLettersServers[bot.GetServerName()] {
            continue
        }

        assignedBots++
        // PROBLEM: Brak weryfikacji powodzenia!
        go bot.AttemptNickChange(nick)
    }
    // ...
}
```

### 2. Bot: Brak informowania NickManagera o niepowodzeniu zmiany nicka

W `bot.go`:
```go
func (b *Bot) ChangeNick(newNick string) {
    if b.IsConnected() {
        oldNick := b.GetCurrentNick()
        util.Info("Bot %s is attempting to change nick to %s", oldNick, newNick)
        b.Connection.Nick(newNick)

        time.Sleep(1 * time.Second)

        // Check if the nick change was successful
        if b.Connection.GetNick() == newNick {
            util.Info("Bot successfully changed nick from %s to %s", oldNick, newNick)

            // Update our internal state to match the connection
            b.mutex.Lock()
            b.CurrentNick = newNick
            b.mutex.Unlock()

            // Notify the nick manager about the change
            if b.nickManager != nil {
                b.nickManager.NotifyNickChange(oldNick, newNick)
            } else {
                util.Warning("NickManager is not set for bot %s", oldNick)
            }
        } else {
            util.Warning("Failed to change nick for bot %s from %s to %s", oldNick, oldNick, newNick)
            // PROBLEM: Brak powiadomienia NickManagera o niepowodzeniu!
        }
    } else {
        util.Debug("Bot %s is not connected; cannot change nick", b.GetCurrentNick())
    }
}
```

### 3. Generowanie ID zapytań ISON oparte na bieżącym nicku bota

W `bot.go`:
```go
func (b *Bot) generateRequestID() string {
    return fmt.Sprintf("%s-%d", b.GetCurrentNick(), time.Now().UnixNano())
}
```

### 4. NickManager: Brak mechanizmu synchronizacji stanu nicków

W `nickmanager.go` brakuje mechanizmu, który by periodycznie sprawdzał czy faktyczne nicki botów zgadzają się z tym, co NickManager o nich sądzi.

## Rozwiązania problemu

### 1. Dodaj informowanie NickManagera o niepowodzeniu zmiany nicka

Zmodyfikuj metodę `ChangeNick` w `bot.go`, aby informowała NickManager o niepowodzeniu:

```go
func (b *Bot) ChangeNick(newNick string) {
    if b.IsConnected() {
        oldNick := b.GetCurrentNick()
        // ... istniejący kod ...
        if b.Connection.GetNick() == newNick {
            // ... istniejący kod sukcesu ...
        } else {
            util.Warning("Failed to change nick for bot %s from %s to %s", oldNick, oldNick, newNick)
            // DODAJ: Poinformuj NickManager o niepowodzeniu
            if b.nickManager != nil {
                b.nickManager.NickChangeFailed(oldNick, newNick)
            }
        }
    } else {
        // ... istniejący kod ...
    }
}
```

### 2. Dodaj metodę obsługi niepowodzeń zmiany nicka w NickManager

Dodaj nową metodę w `nickmanager.go`:

```go
// NickChangeFailed obsługuje sytuację, gdy zmiana nicka bota nie powiodła się
func (nm *NickManager) NickChangeFailed(oldNick, newNick string) {
    nm.mutex.Lock()
    defer nm.mutex.Unlock()
    
    util.Debug("Bot %s failed to change nick to %s, returning nick to pool", oldNick, newNick)
    
    // Usuń z listy tymczasowo niedostępnych nicków, aby był dostępny
    // dla innych botów
    delete(nm.tempUnavailableNicks, strings.ToLower(newNick))
    
    // Możemy dodać nick z powrotem do puli dla innego bota
    if util.IsTargetNick(newNick, nm.nicksToCatch) {
        util.Debug("Nick %s returned to pool after failed change", newNick)
    }
}
```

### 3. Dodaj mechanizm śledzenia oczekiwanych i rzeczywistych nicków botów

Rozszerz strukturę `NickManager` w `nickmanager.go`:

```go
type NickManager struct {
    // ... istniejące pola ...
    
    // Dodaj mapę do śledzenia oczekiwanych nicków botów
    expectedNicks     map[types.Bot]string
    expectedNickMutex sync.RWMutex
}

// W konstruktorze:
func NewNickManager() *NickManager {
    return &NickManager{
        // ... istniejące inicjalizacje ...
        expectedNicks:        make(map[types.Bot]string),
    }
}
```

### 4. Dodaj periodyczne sprawdzanie spójności stanu nicków

Dodaj nową gorutynę w metodzie `Start` w `nickmanager.go`:

```go
func (nm *NickManager) Start() {
    go nm.monitorNicks()
    
    // Dodaj periodyczną weryfikację spójności nicków
    go func() {
        ticker := time.NewTicker(5 * time.Minute)
        for range ticker.C {
            nm.verifyAllBotsNickState()
        }
    }()
}

func (nm *NickManager) verifyAllBotsNickState() {
    nm.mutex.RLock()
    bots := make([]types.Bot, len(nm.bots))
    copy(bots, nm.bots)
    nm.mutex.RUnlock()
    
    for _, bot := range bots {
        if !bot.IsConnected() {
            continue
        }
        
        actualNick := bot.GetCurrentNick()
        
        nm.expectedNickMutex.RLock()
        expectedNick, exists := nm.expectedNicks[bot]
        nm.expectedNickMutex.RUnlock()
        
        if exists && expectedNick != actualNick {
            util.Warning("Nick state inconsistency detected: bot expected to have nick %s but actually has %s", 
                expectedNick, actualNick)
            
            // Synchronizuj stan
            nm.expectedNickMutex.Lock()
            nm.expectedNicks[bot] = actualNick
            nm.expectedNickMutex.Unlock()
            
            // Jeśli oczekiwany nick był nickiem docelowym, zwróć go do puli
            if util.IsTargetNick(expectedNick, nm.GetNicksToCatch()) {
                nm.ReturnNickToPool(expectedNick)
                util.Debug("Returned nick %s to pool during state verification", expectedNick)
            }
        }
    }
}
```

### 5. Modyfikuj obsługę zapytań ISON, aby najpierw weryfikować stan nicków

Zmodyfikuj metodę `monitorNicks` w `nickmanager.go`:

```go
func (nm *NickManager) monitorNicks() {
    // ... istniejący kod ...
    
    for range isonTicker.C {
        // ... istniejący kod wybierania bota ...
        
        bot := connectedBots[localIndex]
        
        // Dodaj weryfikację nicka przed użyciem bota
        actualNick := bot.GetCurrentNick()
        
        nm.expectedNickMutex.RLock()
        expectedNick, exists := nm.expectedNicks[bot]
        nm.expectedNickMutex.RUnlock()
        
        if exists && expectedNick != actualNick {
            util.Warning("Skipping ISON request due to nick inconsistency: expected %s but bot has %s", 
                expectedNick, actualNick)
            
            // Synchronizuj stan
            nm.expectedNickMutex.Lock()
            nm.expectedNicks[bot] = actualNick
            nm.expectedNickMutex.Unlock()
            
            // Jeśli oczekiwany nick był nickiem docelowym, zwróć go do puli
            if util.IsTargetNick(expectedNick, nm.GetNicksToCatch()) {
                nm.ReturnNickToPool(expectedNick)
            }
            
            continue  // Pomiń tę iterację
        }
        
        // Teraz możemy bezpiecznie używać bota
        // ... istniejący kod ISON ...
    }
}
```

### 6. Aktualizuj śledzenie oczekiwanych nicków przy zmianie

Zaktualizuj istniejące metody w `nickmanager.go`:

```go
func (nm *NickManager) NotifyNickChange(oldNick, newNick string) {
    nm.mutex.Lock()
    defer nm.mutex.Unlock()
    
    // ... istniejący kod ...
    
    // Znajdź bota o podanym starym nicku i zaktualizuj oczekiwany nick
    for _, bot := range nm.bots {
        if bot.GetCurrentNick() == newNick {
            nm.expectedNickMutex.Lock()
            nm.expectedNicks[bot] = newNick
            nm.expectedNickMutex.Unlock()
            break
        }
    }
}
```

### 7. Zaktualizuj metodę AttemptNickChange, aby aktualizowała oczekiwane nicki

Kiedy wysyłamy żądanie zmiany nicka, powinniśmy zaktualizować oczekiwany nick:

```go
func (nm *NickManager) handleISONResponse(onlineNicks []string) {
    // ...
    for assignedBots < len(availableBots) && len(availablePriorityNicks) > 0 {
        // ... istniejący kod ...
        
        // Ustaw oczekiwany nick przed wysłaniem żądania
        nm.expectedNickMutex.Lock()
        nm.expectedNicks[bot] = nick
        nm.expectedNickMutex.Unlock()
        
        go bot.AttemptNickChange(nick)
    }
    // ...
}
```


2. **Loguj więcej informacji o stanie podczas problemów** - dodaj bardziej szczegółowe logi w krytycznych miejscach.

3. **Rozważ implementację mechanizmu ponownych prób zmiany nicka** - jeśli zmiana nicka nie powiodła się za pierwszym razem, spróbuj ponownie za jakiś czas.

4. **Dodaj mechanizm wykrywania i naprawiania wycieków goroutines** - długotrwałe problemy mogą wskazywać na wycieki goroutines w kodzie.

## Podsumowanie

Głównym problemem jest brak synchronizacji między faktycznymi nickami botów a stanem przechowywanym w NickManager. Kluczem do rozwiązania jest:

1. Informowanie NickManagera o niepowodzeniach zmiany nicków
2. Śledzenie oczekiwanych i faktycznych nicków botów
3. Periodyczna weryfikacja i synchronizacja stanu nicków
4. Poprawiona obsługa zapytań ISON z uwzględnieniem możliwych niespójności 