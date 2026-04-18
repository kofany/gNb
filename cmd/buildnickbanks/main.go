package main

import (
	"bufio"
	"encoding/gob"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var (
	srcRoot = flag.String("src-root", "/tmp/gNb-wordsrc", "directory containing cloned word source repositories")
	outDir  = flag.String("out-dir", "data", "directory for generated gob files")
)

var leadBlacklist = map[string]struct{}{
	"other": {}, "such": {}, "first": {}, "many": {}, "more": {}, "same": {}, "own": {}, "good": {},
	"different": {}, "high": {}, "social": {}, "much": {}, "important": {}, "small": {}, "most": {},
	"large": {}, "political": {}, "few": {}, "general": {}, "second": {}, "possible": {}, "public": {},
	"last": {}, "american": {}, "several": {}, "human": {}, "right": {}, "early": {}, "certain": {},
	"economic": {}, "least": {}, "common": {}, "present": {}, "next": {}, "local": {}, "international": {},
	"national": {}, "young": {}, "black": {}, "white": {}, "best": {}, "better": {}, "current": {},
	"optional": {}, "textual": {}, "singular": {}, "process": {}, "workflow": {}, "built": {}, "aged": {},
	"hour": {}, "meanest": {}, "through": {}, "immanent": {}, "salutary": {}, "judgment": {}, "terrific": {},
}

var tailBlacklist = map[string]struct{}{
	"time": {}, "people": {}, "way": {}, "life": {}, "years": {}, "world": {}, "man": {}, "state": {},
	"work": {}, "system": {}, "part": {}, "number": {}, "case": {}, "government": {}, "day": {},
	"states": {}, "men": {}, "children": {}, "women": {}, "example": {}, "power": {}, "information": {},
	"development": {}, "order": {}, "group": {}, "place": {}, "use": {}, "data": {}, "law": {},
	"god": {}, "water": {}, "university": {}, "war": {}, "point": {}, "year": {}, "figure": {},
	"process": {}, "fact": {}, "thing": {}, "problem": {}, "business": {}, "money": {}, "history": {},
	"result": {}, "change": {}, "morning": {}, "evening": {}, "night": {}, "month": {}, "week": {},
}

var leadBoosts = []string{
	"silent", "rough", "fuzzy", "brave", "quick", "gritty", "nippy", "plain", "wild", "fancy",
	"sharp", "smooth", "weird", "peachy", "dusty", "stormy", "slick", "neat", "shady", "mellow",
	"misty", "rusty", "snappy", "snug", "steady", "witty", "zesty", "spry", "spruce", "sturdy",
	"smoky", "frosty", "cool", "fiery", "chilly", "sunny", "moody", "dreamy", "snazzy", "mighty",
	"nimble", "tidy", "eager", "jolly", "merry", "candid", "royal", "swift", "fleet", "noble",
	"cosy", "crisp", "dapper", "dusky", "earthy", "fair", "feral", "gentle", "glossy", "grand",
	"hardy", "jaunty", "keen", "lively", "lucky", "lush", "marble", "murky", "navy", "picky",
	"punchy", "quiet", "rapid", "ready", "sleek", "smart", "snug", "sound", "stark", "sultry",
	"sunlit", "taut", "thrifty", "trim", "urbane", "velvet", "vivid", "warm", "wavy", "whimsy",
	"wiry", "worthy", "young", "zippy", "breezy", "clever", "bold", "dusklit", "bright", "odd",
	"ashen", "sable", "silver", "golden", "amber", "cobalt", "scarlet", "crimson", "ivory", "obsidian",
	"copper", "bronze", "lunar", "solar", "arctic", "desert", "forest", "meadow", "cinder", "ember",
	"willow", "timber", "stone", "granite", "slate", "river", "tundra", "prairie", "canyon", "summit",
	"harbor", "marsh", "thunder", "shadow", "nova", "echo", "auric", "barren", "brawny", "calm",
	"charmed", "choosy", "covert", "dapper", "dashing", "dauntless", "dusky", "earnest", "fierce", "finite",
	"frank", "gallant", "gentle", "glimmer", "grand", "hardy", "humble", "jaunty", "kindly", "lively",
	"lofty", "loyal", "lucid", "merry", "mirthful", "noble", "pepper", "polished", "proud", "regal",
	"robust", "savvy", "shrewd", "silken", "sprightly", "stately", "sterling", "stony", "subtle", "swift",
	"tender", "trim", "true", "vigilant", "vital", "wandering", "wary", "whiskered", "whisper", "zealous",
	"nimble", "fleet", "brisk", "jaunty", "snappy", "spunky", "tidy", "zany", "quirky", "mirthful",
}

var softLeadBoosts = []string{
	"ember", "shadow", "willow", "marble", "cinder", "timber", "echo", "nova", "ripple", "drift",
	"signal", "rocket", "comet", "harbor", "meadow", "summit", "ridge", "thicket", "thorn", "torch",
	"quartz", "topaz", "onyx", "opal", "hazel", "juniper", "maple", "lilac", "reef", "shore",
	"bridge", "delta", "dune", "grove", "mesa", "verge", "vortex", "zenith", "anchor", "anvil",
	"arrow", "banner", "barrel", "blade", "brook", "cache", "cliff", "crest", "dagger", "ember",
	"engine", "fable", "flint", "forge", "gale", "glade", "helm", "hollow", "jetty", "jolt",
	"kestrel", "nickel", "pebble", "quiver", "sable", "scarab", "shale", "slate", "sonar", "spur",
	"stone", "tower", "talon", "tracer", "valley", "voyage", "wagon", "whistle", "willow", "zephyr",
	"ranger", "hunter", "raider", "runner", "sailor", "pilot", "walker", "rover", "diver", "tinker",
}

var tailBoosts = []string{
	"bull", "blaze", "troll", "stalk", "crab", "panda", "goat", "rider", "scout", "druid",
	"dragon", "badger", "falcon", "viper", "otter", "raven", "wolf", "beast", "drake", "warden",
	"bison", "beaver", "bobcat", "cougar", "coyote", "crow", "eagle", "ferret", "fox", "gecko",
	"gator", "heron", "hornet", "jaguar", "lobster", "lynx", "manta", "mastiff", "mink", "mole",
	"owl", "panther", "python", "quail", "shark", "stoat", "stork", "swan", "tiger", "walrus",
	"weasel", "whale", "wren", "yak", "zebra", "adder", "condor", "ibex", "lemur", "orca",
	"puma", "ram", "rook", "slug", "toad", "trout", "wyrm", "anchor", "anvil", "arrow",
	"banner", "barrel", "bastion", "blade", "bridge", "brook", "cannon", "cinder", "cliff", "comet",
	"crest", "dagger", "drift", "ember", "engine", "fable", "flint", "forge", "gale", "glade",
	"harbor", "helm", "hollow", "lancer", "lantern", "legend", "meadow", "meteor", "oak", "pike",
	"quiver", "reef", "rocket", "saber", "saddle", "shadow", "shield", "shore", "signal", "skiff",
	"spear", "sprout", "spur", "stone", "summit", "talon", "thicket", "thorn", "torch", "tower",
	"tracer", "valley", "voyage", "wagon", "whistle", "willow", "canyon", "delta", "dune", "forest",
	"grove", "marsh", "mesa", "prairie", "ridge", "river", "timber", "tundra", "vortex", "zenith",
	"ranger", "hunter", "raider", "runner", "sailor", "pilot", "archer", "walker", "rover", "diver",
	"biker", "glider", "tinker", "sentry", "rogue", "shaman", "knight", "bishop", "wizard", "vagabond",
	"caster", "warden", "broker", "caller", "keeper", "marshal", "nomad", "porter", "sherpa", "tracker",
	"bluff", "bramble", "cache", "cobalt", "copper", "echo", "ferrum", "granite", "hazel", "indigo",
	"jetty", "jolt", "juniper", "kestrel", "lilac", "maple", "merlin", "nickel", "onyx", "opal",
	"pebble", "quartz", "sable", "scarab", "shale", "slate", "sonar", "tangent", "thunder", "topaz",
	"turbo", "umbra", "veldt", "verge", "zephyr",
}

func main() {
	flag.Parse()

	leads, tails, idents, err := buildBanks(*srcRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "build nick banks: %v\n", err)
		os.Exit(1)
	}

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "create output dir: %v\n", err)
		os.Exit(1)
	}

	if err := writeGob(filepath.Join(*outDir, "nick_leads.gob"), leads); err != nil {
		fmt.Fprintf(os.Stderr, "write lead gob: %v\n", err)
		os.Exit(1)
	}
	if err := writeGob(filepath.Join(*outDir, "nick_tails.gob"), tails); err != nil {
		fmt.Fprintf(os.Stderr, "write tail gob: %v\n", err)
		os.Exit(1)
	}
	if err := writeGob(filepath.Join(*outDir, "words.gob"), idents); err != nil {
		fmt.Fprintf(os.Stderr, "write words gob: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("generated %d lead words, %d tail words, %d ident words\n", len(leads), len(tails), len(idents))
}

func buildBanks(root string) ([]string, []string, []string, error) {
	adjsPath := filepath.Join(root, "top-english-wordlists", "top_english_adjs_lower_10000.txt")
	nounsPath := filepath.Join(root, "top-english-wordlists", "top_english_nouns_lower_20000.txt")
	commonNounsPath := filepath.Join(root, "Common-English-Nouns", "README.md")

	adjs, err := readWordFile(adjsPath)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(adjs) > 2000 {
		adjs = adjs[:2000]
	}
	nouns, err := readWordFile(nounsPath)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(nouns) > 8000 {
		nouns = nouns[:8000]
	}
	commonNouns, err := readCommonNounsReadme(commonNounsPath)
	if err != nil {
		return nil, nil, nil, err
	}

	leadSet := make(map[string]struct{})
	tailSet := make(map[string]struct{})
	for _, word := range adjs {
		if isLeadCandidate(word) || isGoodAutoLead(word) {
			leadSet[word] = struct{}{}
		}
	}
	for _, word := range nouns {
		if isSoftLeadCandidate(word) {
			leadSet[word] = struct{}{}
		}
	}
	for _, word := range leadBoosts {
		leadSet[word] = struct{}{}
	}
	for _, word := range softLeadBoosts {
		leadSet[word] = struct{}{}
	}
	for _, word := range tailBoosts {
		tailSet[word] = struct{}{}
	}

	identSet := make(map[string]struct{})
	for _, word := range adjs {
		if isIdentCandidate(word) {
			identSet[word] = struct{}{}
		}
	}
	for _, word := range nouns {
		if isIdentCandidate(word) {
			identSet[word] = struct{}{}
		}
	}
	for word := range leadSet {
		if isIdentCandidate(word) {
			identSet[word] = struct{}{}
		}
	}
	for word := range tailSet {
		if isIdentCandidate(word) {
			identSet[word] = struct{}{}
		}
	}
	for _, word := range commonNouns {
		if isIdentCandidate(word) {
			identSet[word] = struct{}{}
		}
	}

	leads := sortedKeys(leadSet)
	tails := sortedKeys(tailSet)
	idents := sortedKeys(identSet)
	return leads, tails, idents, nil
}

func readWordFile(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var words []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		word := normalizeWord(scanner.Text())
		if word == "" {
			continue
		}
		words = append(words, word)
	}
	return words, scanner.Err()
}

func readCommonNounsReadme(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var words []string
	scanner := bufio.NewScanner(file)
	started := false
	for scanner.Scan() {
		line := normalizeWord(scanner.Text())
		if line == "" {
			continue
		}
		if !started {
			if line == "aardvark" {
				started = true
				words = append(words, line)
			}
			continue
		}
		words = append(words, line)
	}
	return words, scanner.Err()
}

func writeGob(path string, values []string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return gob.NewEncoder(file).Encode(values)
}

func sortedKeys(set map[string]struct{}) []string {
	values := make([]string, 0, len(set))
	for value := range set {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}

func normalizeWord(word string) string {
	word = strings.TrimSpace(strings.ToLower(word))
	if word == "" {
		return ""
	}
	for _, r := range word {
		if r < 'a' || r > 'z' {
			return ""
		}
	}
	return word
}

func isLeadCandidate(word string) bool {
	if len(word) < 3 || len(word) > 7 {
		return false
	}
	if _, blocked := leadBlacklist[word]; blocked {
		return false
	}
	if hasAwkwardRun(word) {
		return false
	}
	for _, suffix := range []string{
		"ing", "tion", "ness", "able", "ible", "ical", "istic", "arian", "ward",
		"less", "like", "some", "ious", "eous", "ative", "atory", "esque",
		"ized", "ised", "ally", "ment", "ship", "est",
	} {
		if strings.HasSuffix(word, suffix) {
			return false
		}
	}
	return hasEnoughVowels(word)
}

func isGoodAutoLead(word string) bool {
	if len(word) < 4 || len(word) > 7 {
		return false
	}
	if _, blocked := leadBlacklist[word]; blocked {
		return false
	}
	if !hasEnoughVowels(word) || hasAwkwardRun(word) {
		return false
	}
	for _, suffix := range []string{
		"ic", "al", "ous", "ive", "ary", "ery", "ism", "ist", "logy", "graph", "gram",
		"tion", "sion", "ment", "ness", "less", "able", "ible", "ical", "istic", "ward",
	} {
		if strings.HasSuffix(word, suffix) {
			return false
		}
	}
	score := 0
	for _, suffix := range []string{"y", "ly", "en", "ish", "ed"} {
		if strings.HasSuffix(word, suffix) {
			score++
			break
		}
	}
	for _, prefix := range []string{"br", "cr", "dr", "fr", "gr", "pr", "sh", "sl", "sm", "sn", "st", "sw", "tr", "wh"} {
		if strings.HasPrefix(word, prefix) {
			score++
			break
		}
	}
	for _, nice := range []string{"a", "e", "i", "o", "u", "y"} {
		if strings.Contains(word, nice) {
			score++
			break
		}
	}
	return score >= 2
}

func isSoftLeadCandidate(word string) bool {
	if len(word) < 4 || len(word) > 7 {
		return false
	}
	if !hasEnoughVowels(word) || hasAwkwardRun(word) {
		return false
	}
	for _, suffix := range []string{
		"tion", "sion", "ment", "ness", "ity", "ism", "ance", "ence", "ship", "hood",
		"dom", "ology", "graphy", "tude", "ette", "less", "ward", "ing", "ium", "ist",
		"asm", "sis", "ics", "ette", "ism", "logy", "gram", "scope", "ture", "form",
	} {
		if strings.HasSuffix(word, suffix) {
			return false
		}
	}
	for _, badPrefix := range []string{"anti", "post", "pre", "proto", "micro", "macro", "inter", "trans", "under", "over"} {
		if strings.HasPrefix(word, badPrefix) {
			return false
		}
	}
	return true
}

func isTailCandidate(word string) bool {
	if len(word) < 3 || len(word) > 7 {
		return false
	}
	if _, blocked := tailBlacklist[word]; blocked {
		return false
	}
	if hasAwkwardRun(word) || !hasEnoughVowels(word) {
		return false
	}
	if strings.HasSuffix(word, "s") && !strings.HasSuffix(word, "ss") && !strings.HasSuffix(word, "us") && !strings.HasSuffix(word, "is") {
		return false
	}
	for _, suffix := range []string{
		"tion", "sion", "ment", "ness", "ity", "ism", "ance", "ence", "ship",
		"hood", "dom", "ology", "graphy", "tude", "ette", "less", "ward",
		"ing", "ium", "ist", "asm", "sis", "ics", "ette", "ette", "ette",
	} {
		if strings.HasSuffix(word, suffix) {
			return false
		}
	}
	if strings.HasSuffix(word, "ly") {
		return false
	}
	return true
}

func isIdentCandidate(word string) bool {
	return len(word) >= 3 && len(word) <= 9 && hasEnoughVowels(word) && !hasAwkwardRun(word)
}

func hasEnoughVowels(word string) bool {
	vowels := 0
	for _, r := range word {
		switch r {
		case 'a', 'e', 'i', 'o', 'u', 'y':
			vowels++
		}
	}
	return vowels >= 1 && vowels <= len(word)-1
}

func hasAwkwardRun(word string) bool {
	repeat := 1
	for i := 1; i < len(word); i++ {
		if word[i] == word[i-1] {
			repeat++
			if repeat >= 3 {
				return true
			}
			continue
		}
		repeat = 1
	}
	return strings.Contains(word, "qj") || strings.Contains(word, "jq") || strings.Contains(word, "vv") || strings.Contains(word, "zx")
}
