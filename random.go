package main

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"

	"github.com/darui3018823/dgo"
)

// randomTags are the picks for `!random <tag>`. Each entry is a YouTube
// search query, resolved the same way as `!play`.
var randomTags = map[string][]string{
	"indie": {
		"Arctic Monkeys - Do I Wanna Know",
		"Tame Impala - The Less I Know The Better",
		"MGMT - Electric Feel",
		"Mac DeMarco - Chamber of Reflection",
		"Vampire Weekend - A-Punk",
		"The Strokes - Reptilia",
		"Phoebe Bridgers - Motion Sickness",
		"Alvvays - Archie, Marry Me",
		"Beach House - Space Song",
		"Cage the Elephant - Cigarette Daydreams",
		"Foster the People - Pumped Up Kicks",
		"Two Door Cinema Club - What You Know",
		"Car Seat Headrest - Drunk Drivers/Killer Whales",
		"Mitski - Washing Machine Heart",
		"The Neighbourhood - Sweater Weather",
		"Wallows - Are You Bored Yet",
	},
	"pop": {
		"Dua Lipa - Levitating",
		"The Weeknd - Blinding Lights",
		"Taylor Swift - Shake It Off",
		"Harry Styles - As It Was",
		"Olivia Rodrigo - good 4 u",
		"Lady Gaga - Bad Romance",
		"Carly Rae Jepsen - Call Me Maybe",
		"Sabrina Carpenter - Espresso",
		"Chappell Roan - Good Luck, Babe!",
		"Billie Eilish - bad guy",
		"Katy Perry - Teenage Dream",
		"Lorde - Royals",
		"Charli xcx - 360",
		"Ariana Grande - 7 rings",
		"Bruno Mars - Uptown Funk",
		"Robyn - Dancing On My Own",
	},
	"old": {
		"Queen - Bohemian Rhapsody",
		"Fleetwood Mac - Dreams",
		"ABBA - Dancing Queen",
		"The Beatles - Here Comes The Sun",
		"David Bowie - Starman",
		"Elton John - Rocket Man",
		"Earth, Wind & Fire - September",
		"Toto - Africa",
		"a-ha - Take On Me",
		"Michael Jackson - Billie Jean",
		"Stevie Wonder - Superstition",
		"The Beach Boys - Wouldn't It Be Nice",
		"Frankie Valli - Can't Take My Eyes Off You",
		"Kate Bush - Running Up That Hill",
		"Electric Light Orchestra - Mr. Blue Sky",
		"Simon & Garfunkel - The Boxer",
	},
	"rock": {
		"Nirvana - Smells Like Teen Spirit",
		"Foo Fighters - Everlong",
		"The Killers - Mr. Brightside",
		"Red Hot Chili Peppers - Californication",
		"Radiohead - Creep",
		"Green Day - Basket Case",
		"Led Zeppelin - Immigrant Song",
		"AC/DC - Back In Black",
		"Muse - Plug In Baby",
		"Paramore - Misery Business",
		"The White Stripes - Seven Nation Army",
		"Queens of the Stone Age - No One Knows",
	},
	"rap": {
		"Kendrick Lamar - HUMBLE.",
		"OutKast - Hey Ya!",
		"Eminem - Lose Yourself",
		"Tyler, The Creator - EARFQUAKE",
		"Missy Elliott - Get Ur Freak On",
		"Kanye West - Stronger",
		"A Tribe Called Quest - Can I Kick It",
		"Doja Cat - Say So",
		"Childish Gambino - Redbone",
		"Nicki Minaj - Super Bass",
		"Beastie Boys - Sabotage",
		"MF DOOM - Doomsday",
	},
	"electronic": {
		"Daft Punk - One More Time",
		"Justice - D.A.N.C.E.",
		"Avicii - Levels",
		"Fred again.. - Delilah (pull me out of this)",
		"Disclosure - Latch",
		"Porter Robinson - Language",
		"The Chemical Brothers - Galvanize",
		"Kavinsky - Nightcall",
		"Aphex Twin - Windowlicker",
		"Madeon - Finale",
		"ODESZA - A Moment Apart",
		"Basement Jaxx - Where's Your Head At",
	},
}

// Words mashed together for `!random chaos`, which searches YouTube for
// something like "haunted spaghetti song" and plays whatever comes up.
var (
	chaosWords = []string{
		"purple", "haunted", "tiny", "angry", "sleepy", "cosmic", "wet", "medieval",
		"suspicious", "dancing", "lonely", "electric", "polka", "accordion", "underwater",
		"sad", "turbo", "vintage", "soviet", "lofi", "ancient", "cursed", "funky", "baroque",
	}
	chaosThings = []string{
		"spaghetti", "goose", "toaster", "frog", "cowboy", "submarine", "potato", "lighthouse",
		"pirate", "hamster", "volcano", "robot", "banana", "wizard", "penguin", "bus",
		"cheese", "dinosaur", "moon", "cat", "tractor", "kazoo", "vampire", "pigeon",
	}
	chaosStyles = []string{"song", "music", "theme", "anthem", "remix", "ballad", "jazz", "metal"}
)

const chaosTag = "chaos"

// randomTagNames lists every tag, sorted, with chaos last.
func randomTagNames() []string {
	names := make([]string, 0, len(randomTags)+1)
	for t := range randomTags {
		names = append(names, t)
	}
	slices.Sort(names)
	return append(names, chaosTag)
}

// randomTagAliases maps other words people might type onto a real tag.
var randomTagAliases = map[string]string{
	"oldies": "old", "classic": "old", "classics": "old", "retro": "old",
	"hiphop": "rap", "hip-hop": "rap",
	"edm": "electronic", "dance": "electronic",
	"weird": chaosTag, "random": chaosTag, "wild": chaosTag,
}

// randomPick returns a search query for tag. An empty tag picks a tag at random.
func randomPick(tag string) string {
	if tag == "" {
		names := randomTagNames()
		tag = names[rand.IntN(len(names))]
	}
	if tag == chaosTag {
		return fmt.Sprintf("%s %s %s",
			chaosWords[rand.IntN(len(chaosWords))],
			chaosThings[rand.IntN(len(chaosThings))],
			chaosStyles[rand.IntN(len(chaosStyles))])
	}
	return pickFresh("random:"+tag, randomTags[tag])
}

var (
	recentMu    sync.Mutex
	recentPicks = map[string][]string{} // key → most recent picks, newest last
)

// pickFresh picks a random option, skipping anything picked recently under
// the same key, so the same song doesn't come up twice in a row. Up to half
// the list is held back, so a 16-song list won't repeat within 8 picks.
func pickFresh(key string, options []string) string {
	recentMu.Lock()
	defer recentMu.Unlock()

	recent := recentPicks[key]
	fresh := make([]string, 0, len(options))
	for _, o := range options {
		if !slices.Contains(recent, o) {
			fresh = append(fresh, o)
		}
	}
	if len(fresh) == 0 {
		fresh = options
	}
	pick := fresh[rand.IntN(len(fresh))]

	recent = append(recent, pick)
	if keep := len(options) / 2; len(recent) > keep {
		recent = recent[len(recent)-keep:]
	}
	recentPicks[key] = recent
	return pick
}

func musicRandom(s *dgo.Session, m *dgo.MessageCreate, args []string) {
	tag := ""
	if len(args) > 0 {
		tag = strings.ToLower(args[0])
		if alias, ok := randomTagAliases[tag]; ok {
			tag = alias
		}
		if _, ok := randomTags[tag]; !ok && tag != chaosTag && tag != "any" {
			_, _ = s.ChannelMessageSend(m.ChannelID, fmt.Sprintf(
				"❌ I don't know the tag **%s**. Try one of: `%s`", tag, strings.Join(randomTagNames(), "`, `")))
			return
		}
		if tag == "any" {
			tag = ""
		}
	}

	// Chaos searches sometimes find nothing playable, so line up a few tries.
	queries := []string{randomPick(tag), randomPick(tag), randomPick(tag)}

	note := "🎲 Totally random pick"
	switch tag {
	case "":
	case chaosTag:
		note = "🌀 Pure chaos pick"
	default:
		note = fmt.Sprintf("🎲 Random **%s** pick", tag)
	}
	playQueries(s, m, queries, note)
}
