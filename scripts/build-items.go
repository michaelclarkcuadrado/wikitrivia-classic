// Rebuild public/items.json.gz from the latest Wikidata JSON dump.
//
// The output is a gzipped JSON object using a columnar/dictionary-encoded
// layout (see emitPacked) so the client can fetch a much smaller file and
// expand it via DecompressionStream + a tiny mapping pass.
//
// Usage: go run scripts/build-items.go
//
// Env:
//   WIKIDATA_DUMP_PATH   where to keep the dump (default: .cache/wikidata-latest-all.json.bz2)
//   OUTPUT_PATH          output file (default: public/items.json.gz)
//   DECOMPRESSOR         lbzip2 (default), pbzip2, or bzip2
//   SITELINKS_THRESHOLD  popularity gate (default: 30)
//   HUMAN_DOB_CUTOFF     humans born after this year are placed by death year (default: 1849)
//   WORKERS              parallel JSON workers (default: NumCPU-1)
//   SKIP_DOWNLOAD        set to 1 to use the existing dump file as-is
//
// Single pass over the dump: parallel JSON workers parse each entity once,
// record its English label in a per-worker label map, and emit a candidate if
// it matches V1's selection rules. After the stream finishes, resolve each
// candidate's raw P31/P106 QIDs against the union of per-worker maps.
//
// Memory: stores ~110M (qid, enLabel) pairs in RAM (~12 GB total across all
// workers). Trades RAM for not having to decompress the dump twice.

package main

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const dumpURL = "https://dumps.wikimedia.org/wikidatawiki/entities/latest-all.json.bz2"

var (
	dumpPath           = envOr("WIKIDATA_DUMP_PATH", ".cache/wikidata-latest-all.json.bz2")
	outputPath         = envOr("OUTPUT_PATH", "public/items.json.gz")
	decompressor       = envOr("DECOMPRESSOR", "lbzip2")
	sitelinksThreshold = envInt("SITELINKS_THRESHOLD", 30)
	// Items lacking a P18 image must clear a higher sitelinks bar (proxy
	// for notability) unless their instance_of matches imageOptionalKeywords.
	noImageSitelinksThreshold = envInt("NO_IMAGE_SITELINKS_THRESHOLD", 60)
	humanDOBCutoff            = envInt("HUMAN_DOB_CUTOFF", 1849)
	numWorkers                = envInt("WORKERS", max(1, runtime.NumCPU()-1))
	skipDownload              = os.Getenv("SKIP_DOWNLOAD") != ""
)

const (
	qHuman      = "Q5"
	pInstanceOf = "P31"
	pOccupation = "P106"
	pImage      = "P18"
	pBirth      = "P569"
	pDeath      = "P570"
)

// imageFallbackProps are tried in order when P18 is missing. Many historical
// countries, dynasties, and orgs have a flag/coat-of-arms/logo but no P18 —
// without these fallbacks the card renders as a broken image.
var imageFallbackProps = []string{
	pImage, // P18 image (canonical)
	"P154", // logo image (orgs, brands)
	"P94",  // coat of arms image (countries, dynasties)
	"P41",  // flag image (countries, states)
	"P158", // seal image
}

var nonhumanDatePriority = []string{
	"P582", "P576", "P1619", "P577", "P1191",
	"P575", "P580", "P571", "P1249", "P6949", "P1319",
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func envInt(k string, d int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return d
}

// Outer parse: keeps Labels/Descriptions to just the English entry (struct
// fields silently skip non-matching JSON keys, vs. a map[string]X that
// allocates every language). Sitelinks values stay as RawMessage so we don't
// parse 100+ titles per major entity. Claims stay as RawMessage so we don't
// pay the per-claim parse cost on the ~99% of entities that get rejected by
// the sitelinks / label / description gates.
type entity struct {
	Type         string                     `json:"type"`
	ID           string                     `json:"id"`
	Labels       enLang                     `json:"labels"`
	Descriptions enLang                     `json:"descriptions"`
	Sitelinks    map[string]json.RawMessage `json:"sitelinks"`
	Claims       json.RawMessage            `json:"claims"`
}
type enLang struct {
	En monoValue `json:"en"`
}
type monoValue struct {
	Value string `json:"value"`
}
type sitelink struct {
	Title string `json:"title"`
}
type claim struct {
	Rank     string `json:"rank"`
	Mainsnak struct {
		Datavalue struct {
			Type  string          `json:"type"`
			Value json.RawMessage `json:"value"`
		} `json:"datavalue"`
	} `json:"mainsnak"`
}
type timeValue struct {
	Time      string `json:"time"`
	Precision int    `json:"precision"`
}
type idValue struct {
	ID string `json:"id"`
}

type candidate struct {
	ID             string
	Label          string
	Description    string
	EnwikiTitle    string
	Image          string // empty if entity has no P18; resolveAndFilter decides whether to keep
	Sitelinks      int
	Year           int
	PropID         string
	InstanceOfQids []string
	OccupationQids []string // nil for non-humans, may be empty for humans w/o P106
	// Populated after the scan by resolveAndFilter.
	InstanceOf  []string  // resolved English labels, with excluded classes removed
	Occupations *[]string // resolved English labels (nil for non-humans)
}

// excludedClassKeywords: case-insensitive substring keywords. If ANY of the
// item's instance_of labels contains one of these, the entire item is
// dropped — not just that class. Wikidata's geographic vocabulary has huge
// cardinality ("comune of Italy", "Hanseatic city", "border town", "rural
// district of Saxony", "federal subject of Russia", ...), so we group by
// broad keywords rather than enumerating every variant. Earlier narrow
// patterns ("city of", "city in") missed bare "city" / "big city" /
// "metropolis" / etc. and let thousands of cities through.
//
// Famous cities (Paris, NYC, Berlin, Tokyo) are intentionally dropped: they
// always carry at least one matching class ("city", "metropolis",
// "city-state", ...) and the dataset overrepresents them anyway.
var excludedClassKeywords = []string{
	// Settlements.
	"city", "town", "village", "borough", "hamlet", "suburb",
	"neighborhood", "neighbourhood",
	"locality",
	"commune",                 // French communes
	"comune",                  // Italian comuni (single m)
	"municipality", "municipal",
	"metropolis",
	"urban area", "rural area",
	"census-designated", "unincorporated",

	// Administrative subdivisions.
	"county", "parish",
	"arrondissement", "subprefecture", "subdistrict",
	"district", "prefecture", "province",
	"department of", // French départements (avoid catching "department store")
	"region of",     // region of France (avoid catching "H II region")
	"raion", "oblast", "krai", "okrug",
	"powiat", "special ward",
	"federal subject",
	"administrative territorial",
	"administrative division",
	"administrative center", "administrative centre",
	"administrative entity",
	"administrative region",

	// Astronomical catalog entries make poor trivia. Exoplanets and comets
	// are intentionally not on this list — they're kept.
	"galaxy", "asteroid",
	"nebula",
	"open cluster", "globular cluster",
	"infrared source", "radio source", "x-ray source",
	"moon of", "irregular moon", // moon of Jupiter/Saturn/...

	// Aviation infrastructure: airports/aerodromes are bot-generated.
	"airport", "aerodrome",

	// Sports catalog: clubs, leagues, seasons, tournaments. Bot-generated.
	// Stadiums and the Olympic Games proper survive — only the catalog
	// auto-pages (per-club, per-season, per-discipline) are filtered.
	"association football",
	"sports season",
	"sporting event",
	"olympic sports discipline",
	"formula one",     // F1 teams / Grand Prix / seasons
	"tour de france",
	"edition",         // "Eurovision Song Contest edition", "tennis tournament edition", "edition of the UEFA Champions League", "Summer Olympic Games edition", ...

	// Wikipedia/Wikimedia meta.
	"wikipedia",

	// Scientific journal catalog.
	"open-access", // open-access publisher

	// Time-unit items make tautological trivia ("when did 1991 happen?").
	"decade", "calendar year", "century",
}

func hasExcludedClass(labels []string) bool {
	for _, l := range labels {
		ll := strings.ToLower(l)
		for _, kw := range excludedClassKeywords {
			if strings.Contains(ll, kw) {
				return true
			}
		}
	}
	return false
}

// imageOptionalKeywords: instance_of substring keywords for classes that may
// pass the build at the standard sitelinks threshold even without a P18
// image. Targets history-rich classes that systematically lack a canonical
// Wikidata image (Egyptian dynasties, Hellenistic kingdoms, Holy Roman
// Empire states, colonies, biblical figures, ...) but make great trivia.
var imageOptionalKeywords = []string{
	"dynasty",          // Egyptian dynasty, Chinese dynasty, ...
	"kingdom",          // Hellenistic kingdom, barbarian kingdom, ...
	"empire",           // colonial empire, Hellenistic empire, ...
	"colony", "colonial",
	"vassal", "tributary",
	"historical country",
	"historical period",
	"historical region",
	"state in the holy roman empire",
	"ecumenical council",
	"biblical figure",
}

func hasImageOptionalClass(labels []string) bool {
	for _, l := range labels {
		ll := strings.ToLower(l)
		for _, kw := range imageOptionalKeywords {
			if strings.Contains(ll, kw) {
				return true
			}
		}
	}
	return false
}

// Curated QID blocklist applied at emit time. These survive the dump-time
// class/keyword filters but make poor cards (giveaway questions, NSFW,
// content that conflicts with the game's tone).
var badCards = map[string]bool{
	"Q745019":   true, // Colt's Manufacturing Company
	"Q697675":   true, // Gigabyte Technology
	"Q157064":   true, // Puma (brand)
	"Q179900":   true, // David (Michelangelo)
	"Q218567":   true, // Law & Order SVU
	"Q486682":   true, // Crips
	"Q12871":    true, // Simon the Zealot
	"Q5505":     true, // Lake Victoria
	"Q58784":    true, // Das Kapital
	"Q345":      true, // Virgin Mary
	"Q184742":   true, // Metamorphoses
	"Q128267":   true, // Joseph
	"Q60220":    true, // Aeneid
	"Q25716":    true, // 1st millennium BC
	"Q134862":   true, // Champagne
	"Q207193":   true, // Skellig Michael
	"Q38526":    true, // 1,000,000
	"Q193159":   true, // Russian Armed Forces
	"Q43343":    true, // Folk music
	"Q10282403": true, // Surgical mask
	"Q46197":    true, // Ascension
	"Q132851":   true, // Admiral
	"Q55629":    true, // Epsom Derby
	"Q828435":   true, // Spanish conquest of the Aztec Empire
	"Q460584":   true, // Scala
	"Q244157":   true, // Igbo people
	"Q1990219":  true, // French colonization of the Americas
	"Q321303":   true, // The Garden of Earthly Delights
	"Q9730":     true, // Classical music
	"Q221062":   true, // DuPont
	"Q39427":    true, // Surrealism
	"Q219995":   true, // Guanches
	"Q833163":   true, // Knight Bachelor
	"Q42233":    true, // Sickle
	"Q80290":    true, // Forbidden City
	"Q39950":    true, // Vedas
	"Q468836":   true, // Raedwald of East Anglia
	"Q73801":    true, // Xbox Game Studios
	"Q81018":    true, // Judas Iscariot
	"Q528187":   true, // Pringles
	"Q187846":   true, // Russian Alphabet
	"Q99309":    true, // Pantheon, Rome
	"Q55":       true, // Netherlands
	"Q80344":    true, // Mount Olympus
	"Q161718":   true, // United Nations Development Programme
	"Q3293295":  true, // Turkish Naval Forces
	"Q1059358":  true, // Rubáiyát of Omar Khayyám
	"Q82996":    true, // Runes
	"Q106187":   true, // Giant's Causeway
	"Q213804":   true, // Lindisfarne
	"Q229702":   true, // Ham (son of Noah)
	"Q830183":   true, // Eve
	"Q44996":    true, // The Oxford English Dictionary
	"Q212746":   true, // Anglo-Saxon Chronicle
	"Q41726":    true, // Freemasonry
	"Q142":      true, // France
	"Q115":      true, // Ethiopia
	"Q12263":    true, // Mahjong
	"Q213633":   true, // Deborah
	"Q7734":     true, // Joshua
	"Q202466":   true, // Blond
	"Q94787":    true, // Sunflower Oil
	"Q174640":   true, // V-2 Rocket
	"Q1616457":  true, // Mycroft Holmes
	"Q302":      true, // Jesus
	"Q84422877": true, // Samaritan woman
	"Q917374":   true, // Norwegian Armed Forces
	"Q718":      true, // Chess
	"Q183":      true, // Germany
	"Q38":       true, // Italy
	"Q1649955":  true, // Scarlett O'Hara
	"Q1768161":  true, // Abraham in Islam
	"Q11768":    true, // Ancient Egypt
	"Q304673":   true, // Platoon
	"Q43982":    true, // Bartholomew the Apostle
	"Q36":       true, // Poland
	"Q47128":    true, // Christmas Tree
	"Q242382":   true, // Thusnelda
	"Q1069785":  true, // Hong Kong Flu
	"Q183562":   true, // Umayyad Mosque
	"Q82613":    true, // Krakatoa
	"Q459188":   true, // Kingdom of the Isles
	"Q465283":   true, // Russian Navy
	"Q461606":   true, // Arsène Lupin
	"Q304690":   true, // Li Ching-Yuen
	"Q2001966":  true, // Company rule in India
	"Q651532":   true, // The Three Little Pigs
	"Q184661":   true, // Ogham
	"Q31057":    true, // Norfolk Island
	"Q1246283":  true, // Kumbhalgarh
	"Q735349":   true, // Russian conquest of Siberia
	"Q2223341":  true, // Elizabeth Bennet
	"Q40185":    true, // Divine Comedy
	"Q2723024":  true, // Enron scandal
	"Q133600":   true, // Banksy
	"Q14112":    true, // Corsica
	"Q1892745":  true, // Salvator Mundi (Leonardo)
	"Q994776":   true, // Brutalist architecture
	"Q182865":   true, // War in Afghanistan
	"Q936394":   true, // Pornhub
	"Q466683":   true, // Chyna
	"Q824540":   true, // AVN Awards
	"Q19559884": true, // August Ames
	"Q2709":     true, // Sasha Grey
	"Q260794":   true, // Sunny Leone
	"Q973475":   true, // Dustin Diamond
	"Q3700050":  true, // XVideos
	"Q18735049": true, // Mia Khalifa
	"Q233118":   true, // Traci Lords
	"Q3916703":  true, // Riley Reid
	"Q18749736": true, // Johnny Sins
	"Q65115154": true, // Belle Delphine
	"Q739550":   true, // M&M's
	"Q1431121":  true, // St Michael's Mount
	"Q174097":   true, // Hogwarts
	"Q8690":     true, // Cultural Revolution
	"Q149086":   true, // Homicide
}

var centuryRE = regexp.MustCompile(`(?i)(?:th|st|nd)[ -]century`)

// shouldKeep drops cards that give away their answer or are on the curated
// blocklist. Mirrors the filters that used to live in components/game.tsx —
// applying them at build time saves bandwidth and removes runtime work.
func shouldKeep(c *candidate) bool {
	ys := strconv.Itoa(c.Year)
	if strings.Contains(c.Label, ys) {
		return false
	}
	if strings.Contains(c.Description, ys) {
		return false
	}
	if centuryRE.MatchString(c.Description) {
		return false
	}
	if badCards[c.ID] {
		return false
	}
	return true
}

// interner assigns a stable integer index to each unique string it sees,
// in first-seen order. Used to dictionary-encode the low-cardinality
// fields (date_prop_id, instance_of, occupations).
type interner struct {
	m    map[string]int
	list []string
}

func (it *interner) index(s string) int {
	if it.m == nil {
		it.m = make(map[string]int)
	}
	if i, ok := it.m[s]; ok {
		return i
	}
	i := len(it.list)
	it.m[s] = i
	it.list = append(it.list, s)
	return i
}

var timeRE = regexp.MustCompile(`^([+-])0*(\d+)`)

func parseTimeYear(raw json.RawMessage) (int, bool) {
	var tv timeValue
	if err := json.Unmarshal(raw, &tv); err != nil {
		return 0, false
	}
	if tv.Precision < 9 {
		return 0, false
	}
	m := timeRE.FindStringSubmatch(tv.Time)
	if m == nil {
		return 0, false
	}
	y, err := strconv.Atoi(m[2])
	if err != nil {
		return 0, false
	}
	if m[1] == "-" {
		y = -y
	}
	return y, true
}

func firstClaimYear(claims []json.RawMessage) (int, bool) {
	for _, raw := range claims {
		var c claim
		if err := json.Unmarshal(raw, &c); err != nil {
			continue
		}
		if c.Rank == "deprecated" {
			continue
		}
		if c.Mainsnak.Datavalue.Type != "time" {
			continue
		}
		if y, ok := parseTimeYear(c.Mainsnak.Datavalue.Value); ok {
			return y, true
		}
	}
	return 0, false
}

func qidValues(claims []json.RawMessage) []string {
	out := make([]string, 0, len(claims))
	for _, raw := range claims {
		var c claim
		if err := json.Unmarshal(raw, &c); err != nil {
			continue
		}
		if c.Rank == "deprecated" {
			continue
		}
		var iv idValue
		if err := json.Unmarshal(c.Mainsnak.Datavalue.Value, &iv); err != nil {
			continue
		}
		if iv.ID != "" {
			out = append(out, iv.ID)
		}
	}
	return out
}

func firstStringValue(claims []json.RawMessage) string {
	for _, raw := range claims {
		var c claim
		if err := json.Unmarshal(raw, &c); err != nil {
			continue
		}
		if c.Rank == "deprecated" {
			continue
		}
		var s string
		if err := json.Unmarshal(c.Mainsnak.Datavalue.Value, &s); err != nil {
			continue
		}
		if s != "" {
			return s
		}
	}
	return ""
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func pickDate(claims map[string][]json.RawMessage, isHuman bool) (int, string, bool) {
	if isHuman {
		dob, dobOk := firstClaimYear(claims[pBirth])
		if dobOk && dob <= humanDOBCutoff {
			return dob, pBirth, true
		}
		if dod, ok := firstClaimYear(claims[pDeath]); ok {
			return dod, pDeath, true
		}
		if dobOk {
			return dob, pBirth, true
		}
		return 0, "", false
	}
	for _, p := range nonhumanDatePriority {
		if y, ok := firstClaimYear(claims[p]); ok {
			return y, p, true
		}
	}
	return 0, "", false
}

func buildCandidateFromEntity(e *entity) *candidate {
	// Cheap gates first — they reject ~99% of entities before we touch claims.
	if len(e.Sitelinks) < sitelinksThreshold {
		return nil
	}
	enwikiRaw, ok := e.Sitelinks["enwiki"]
	if !ok {
		return nil
	}
	label := e.Labels.En.Value
	desc := e.Descriptions.En.Value
	if label == "" || desc == "" {
		return nil
	}
	var sl sitelink
	if err := json.Unmarshal(enwikiRaw, &sl); err != nil || sl.Title == "" {
		return nil
	}

	// Now pay for parsing claims.
	var claims map[string][]json.RawMessage
	if err := json.Unmarshal(e.Claims, &claims); err != nil {
		return nil
	}

	instOf := qidValues(claims[pInstanceOf])
	if len(instOf) == 0 {
		return nil
	}
	// Try P18 first, then logo/CoA/flag/seal fallbacks. Image-less items
	// reach resolveAndFilter, which applies the history allowlist and the
	// tiered sitelinks fallback to decide whether to keep them.
	var image string
	for _, p := range imageFallbackProps {
		if image = firstStringValue(claims[p]); image != "" {
			break
		}
	}
	isHuman := contains(instOf, qHuman)
	year, propID, ok := pickDate(claims, isHuman)
	if !ok {
		return nil
	}
	var occs []string
	if isHuman {
		occs = qidValues(claims[pOccupation])
		if occs == nil {
			occs = []string{} // distinguish "human, no occupations" from non-human
		}
	}
	return &candidate{
		ID:             e.ID,
		Label:          label,
		Description:    desc,
		EnwikiTitle:    sl.Title,
		Image:          strings.ReplaceAll(image, " ", "_"),
		Sitelinks:      len(e.Sitelinks),
		Year:           year,
		PropID:         propID,
		InstanceOfQids: instOf,
		OccupationQids: occs,
	}
}

// streamLines spawns the decompressor and invokes handle on every entity line.
// It strips the bracket and trailing-comma framing of the dump's JSON-array form.
// Each call to handle gets a fresh []byte the worker can keep.
func streamLines(handle func([]byte)) error {
	cmd := exec.Command(decompressor, "-dc", dumpPath)
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	br := bufio.NewReaderSize(stdout, 4<<20)
	for {
		line, err := readLine(br)
		if len(line) > 0 {
			if !(len(line) < 2 || line[0] == '[' || line[0] == ']') {
				if line[len(line)-1] == ',' {
					line = line[:len(line)-1]
				}
				handle(line)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			cmd.Process.Kill()
			cmd.Wait()
			return err
		}
	}
	return cmd.Wait()
}

// readLine reads up to and including the next '\n'. Returns the line without
// the trailing newline as a freshly allocated slice. No size cap (Wikidata
// entity lines can be megabytes).
func readLine(br *bufio.Reader) ([]byte, error) {
	var full []byte
	for {
		chunk, err := br.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			full = append(full, chunk...)
			continue
		}
		if err != nil && err != io.EOF {
			return full, err
		}
		// Trim trailing \n.
		if len(chunk) > 0 && chunk[len(chunk)-1] == '\n' {
			chunk = chunk[:len(chunk)-1]
		}
		if full == nil {
			cp := make([]byte, len(chunk))
			copy(cp, chunk)
			return cp, err
		}
		full = append(full, chunk...)
		return full, err
	}
}

type workerState struct {
	labels     map[string]string
	candidates []*candidate
}

// passAll streams the dump once. Each worker parses lines off a shared channel
// and records (a) the entity's English label in its OWN local map and (b) any
// matching candidate in its OWN local slice. No locking. After the stream
// closes, the per-worker label maps stay separate — resolveLabel walks all of
// them at emit time. Avoiding a final merge keeps peak memory at ~12 GB
// instead of ~24 GB.
func passAll() ([]*candidate, []map[string]string, error) {
	linesCh := make(chan []byte, 4096)
	states := make([]workerState, numWorkers)
	for i := range states {
		// Pre-size each shard map to ~equal slice of total entities to skip
		// early rehashes during the hot loop.
		states[i].labels = make(map[string]string, 4_000_000)
	}
	var wg sync.WaitGroup
	var nCandidates int64

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		st := &states[i]
		go func() {
			defer wg.Done()
			for line := range linesCh {
				var e entity
				if err := json.Unmarshal(line, &e); err != nil {
					continue
				}
				if e.Type != "item" || e.ID == "" {
					continue
				}
				if lbl := e.Labels.En.Value; lbl != "" {
					st.labels[e.ID] = lbl
				}
				if c := buildCandidateFromEntity(&e); c != nil {
					atomic.AddInt64(&nCandidates, 1)
					st.candidates = append(st.candidates, c)
				}
			}
		}()
	}

	var n int64
	t0 := time.Now()
	err := streamLines(func(b []byte) {
		n++
		if n%1_000_000 == 0 {
			elapsed := time.Since(t0).Seconds()
			fmt.Fprintf(os.Stderr,
				"[build-items] scanned %d entities, %d candidates (%.0fk lines/s)\n",
				n, atomic.LoadInt64(&nCandidates), float64(n)/elapsed/1000)
		}
		linesCh <- b
	})
	close(linesCh)
	wg.Wait()

	var allCands []*candidate
	shards := make([]map[string]string, numWorkers)
	for i := range states {
		allCands = append(allCands, states[i].candidates...)
		shards[i] = states[i].labels
	}

	if err != nil {
		return nil, nil, err
	}
	return allCands, shards, nil
}

// resolveAndFilter resolves each candidate's instance_of and occupation QIDs
// to English labels, drops candidates whose classes hit the blocklist, and
// applies the image gate. Image-less candidates pass only if their class
// matches the history allowlist OR they clear noImageSitelinksThreshold.
func resolveAndFilter(cands []*candidate, shards []map[string]string) []*candidate {
	out := make([]*candidate, 0, len(cands))
	for _, c := range cands {
		labels := resolveLabels(c.InstanceOfQids, shards)
		if len(labels) == 0 || hasExcludedClass(labels) {
			continue
		}
		if c.Image == "" {
			if !hasImageOptionalClass(labels) && c.Sitelinks < noImageSitelinksThreshold {
				continue
			}
		}
		c.InstanceOf = labels
		if c.OccupationQids != nil {
			occs := resolveLabels(c.OccupationQids, shards)
			c.Occupations = &occs
		}
		out = append(out, c)
	}
	return out
}

func resolveLabels(qids []string, shards []map[string]string) []string {
	out := make([]string, 0, len(qids))
	for _, q := range qids {
		for _, m := range shards {
			if l, ok := m[q]; ok && l != "" {
				out = append(out, l)
				break
			}
		}
	}
	return out
}

// emitPacked writes the gzipped, columnar/dictionary-encoded wire format.
//
// Layout:
//
//	{
//	  "v": 1,
//	  "fields": ["id","label","year","description","image",
//	             "wikipedia_title","date_prop_id","instance_of","occupations"],
//	  "dicts":  { "date_prop_id":[...], "instance_of":[...], "occupations":[...] },
//	  "rows":   [ [idInt, label, year, description, image,
//	               wikiTitle, dpIdx, instIdxs, occIdxs|null], ... ]
//	}
//
// Compactness tricks:
//   - id stored as integer ("Q" prefix re-added on the client)
//   - wikipedia_title stored as "" when it equals label (the common case)
//   - low-cardinality string fields replaced by indices into per-field dicts
//   - rows sorted by numeric QID for stable, diff-friendly output
func emitPacked(cands []*candidate) (int, error) {
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return 0, err
	}

	sort.Slice(cands, func(i, j int) bool {
		ai, _ := strconv.Atoi(strings.TrimPrefix(cands[i].ID, "Q"))
		aj, _ := strconv.Atoi(strings.TrimPrefix(cands[j].ID, "Q"))
		return ai < aj
	})

	dpID := &interner{}
	inst := &interner{}
	occ := &interner{}

	rows := make([][]interface{}, 0, len(cands))
	dropped := 0
	for _, c := range cands {
		if !shouldKeep(c) {
			dropped++
			continue
		}
		idInt, err := strconv.Atoi(strings.TrimPrefix(c.ID, "Q"))
		if err != nil {
			return 0, fmt.Errorf("bad QID %q: %w", c.ID, err)
		}
		wikiTitle := c.EnwikiTitle
		if wikiTitle == c.Label {
			wikiTitle = ""
		}
		instIdxs := make([]int, len(c.InstanceOf))
		for j, s := range c.InstanceOf {
			instIdxs[j] = inst.index(s)
		}
		var occVal interface{}
		if c.Occupations == nil {
			occVal = nil
		} else {
			occIdxs := make([]int, len(*c.Occupations))
			for j, s := range *c.Occupations {
				occIdxs[j] = occ.index(s)
			}
			occVal = occIdxs
		}
		rows = append(rows, []interface{}{
			idInt,
			c.Label,
			c.Year,
			c.Description,
			c.Image,
			wikiTitle,
			dpID.index(c.PropID),
			instIdxs,
			occVal,
		})
	}

	packed := map[string]interface{}{
		"v": 1,
		"fields": []string{
			"id", "label", "year", "description", "image",
			"wikipedia_title", "date_prop_id", "instance_of", "occupations",
		},
		"dicts": map[string]interface{}{
			"date_prop_id": dpID.list,
			"instance_of":  inst.list,
			"occupations":  occ.list,
		},
		"rows": rows,
	}

	f, err := os.Create(outputPath)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	gz, err := gzip.NewWriterLevel(f, gzip.BestCompression)
	if err != nil {
		return 0, err
	}
	defer gz.Close()

	enc := json.NewEncoder(gz)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(packed); err != nil {
		return 0, err
	}

	fmt.Fprintf(os.Stderr,
		"[build-items] kept %d, dropped %d (badCards/year-in-label-or-desc/century)\n",
		len(rows), dropped)
	fmt.Fprintf(os.Stderr,
		"[build-items] dict sizes: date_prop_id=%d instance_of=%d occupations=%d\n",
		len(dpID.list), len(inst.list), len(occ.list))

	return len(rows), nil
}

func downloadDump() error {
	if err := os.MkdirAll(filepath.Dir(dumpPath), 0o755); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "[build-items] downloading %s → %s (~100 GB, resumable)\n", dumpURL, dumpPath)
	cmd := exec.Command("curl", "-L", "--fail", "-C", "-", "-o", dumpPath, dumpURL)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func main() {
	t0 := time.Now()

	if !skipDownload {
		if _, err := os.Stat(dumpPath); errors.Is(err, os.ErrNotExist) {
			if err := downloadDump(); err != nil {
				fmt.Fprintln(os.Stderr, "[build-items] download failed:", err)
				os.Exit(1)
			}
		}
	}
	if _, err := os.Stat(dumpPath); err != nil {
		fmt.Fprintln(os.Stderr, "[build-items] dump not found:", dumpPath)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "[build-items] %d workers, decompressor=%s\n", numWorkers, decompressor)
	fmt.Fprintf(os.Stderr, "[build-items] scanning %s\n", dumpPath)
	candidates, shards, err := passAll()
	if err != nil {
		fmt.Fprintln(os.Stderr, "[build-items] scan failed:", err)
		os.Exit(1)
	}
	totalLabels := 0
	for _, m := range shards {
		totalLabels += len(m)
	}
	fmt.Fprintf(os.Stderr, "[build-items] scan done: %d candidates, %d labels (%.0fs)\n",
		len(candidates), totalLabels, time.Since(t0).Seconds())

	before := len(candidates)
	candidates = resolveAndFilter(candidates, shards)
	fmt.Fprintf(os.Stderr, "[build-items] geographic blocklist: kept %d / %d candidates\n",
		len(candidates), before)

	written, err := emitPacked(candidates)
	if err != nil {
		fmt.Fprintln(os.Stderr, "[build-items] emit failed:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "[build-items] done: wrote %d items to %s in %.0fs\n",
		written, outputPath, time.Since(t0).Seconds())
}
