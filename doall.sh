#!/bin/bash

# Ścieżka do katalogu głównego projektu
PROJECT_DIR="/Users/kfn/gognb"

# Plik, do którego będą zapisywane wszystkie zawartości
OUTPUT_FILE="$PROJECT_DIR/all.txt"

# Tworzenie pustego pliku wyjściowego lub wyczyszczenie istniejącego
> "$OUTPUT_FILE"

# Funkcja przetwarzająca pliki w katalogu
process_files() {
    local dir_path="$1"
    for file in "$dir_path"/*; do
        if [ "$file" != "$OUTPUT_FILE" ]; then
            if [ -f "$file" ]; then
                echo "# Plik $file" >> "$OUTPUT_FILE"
                cat "$file" >> "$OUTPUT_FILE"
                echo -e "\n# Koniec $file\n" >> "$OUTPUT_FILE"
            elif [ -d "$file" ]; then
                process_files "$file"
            fi
        fi
    done
}

# Przetwarzanie wszystkich plików w katalogu głównym projektu
process_files "$PROJECT_DIR"

echo "Zawartość wszystkich plików została zapisana do $OUTPUT_FILE"
