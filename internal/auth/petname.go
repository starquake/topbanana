package auth

import (
	"crypto/rand"
	"math/big"
)

// GeneratePetname returns an "Adjective-Adjective-Noun" identifier
// (e.g. "Steamy-Farty-Bear"). Not guaranteed unique - callers handle
// the UNIQUE collision with a retry loop.
func GeneratePetname() string {
	a1 := pickRandom(petnameAdjectives)
	a2 := pickRandom(petnameAdjectives)
	n := pickRandom(petnameNouns)

	return a1 + "-" + a2 + "-" + n
}

// pickRandom returns a uniformly-random element from words. Falls
// back to words[0] on the (effectively impossible) crypto/rand error
// rather than taking down the request path.
func pickRandom(words []string) string {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(words))))
	if err != nil {
		return words[0]
	}

	return words[n.Int64()]
}

// petnameAdjectives is the title-cased dictionary used by GeneratePetname.
//
//nolint:gochecknoglobals // dictionary table; values never mutate.
var petnameAdjectives = []string{
	"Ancient", "Angry", "Bashful", "Beefy", "Bendy", "Blissful", "Bold",
	"Bouncy", "Brave", "Breezy", "Bright", "Bubbly", "Bumpy", "Burly",
	"Busy", "Buzzy", "Calm", "Cheeky", "Cheerful", "Chilly", "Chirpy",
	"Chonky", "Chubby", "Chunky", "Classy", "Clever", "Cloudy", "Clumsy",
	"Cosmic", "Cosy", "Cranky", "Crispy", "Crunchy", "Cuddly", "Curious",
	"Curly", "Dainty", "Dapper", "Daring", "Dazzling", "Dippy", "Dizzy",
	"Doughy", "Dreamy", "Drowsy", "Dusty", "Eager", "Earnest", "Easy",
	"Electric", "Epic", "Fancy", "Farty", "Feisty", "Fierce", "Fiery",
	"Fizzy", "Flaky", "Flappy", "Floppy", "Fluffy", "Foggy", "Frilly",
	"Frisky", "Frosty", "Funky", "Funny", "Fuzzy", "Gentle", "Giddy",
	"Giggly", "Glittery", "Gloomy", "Glowing", "Glum", "Goofy", "Grand",
	"Greasy", "Greedy", "Grumpy", "Hairy", "Handy", "Happy", "Hasty",
	"Hearty", "Hefty", "Honest", "Hoppy", "Humble", "Hungry", "Husky",
	"Icy", "Itchy", "Jaunty", "Jazzy", "Jiggly", "Jolly", "Jumpy",
	"Kindly", "Lanky", "Lazy", "Leaky", "Lively", "Loopy", "Lucky",
	"Lumpy", "Magical", "Manic", "Melted", "Mellow", "Merry", "Mighty",
	"Misty", "Moody", "Mossy", "Muddy", "Muffled", "Mushy", "Mystic",
	"Nappy", "Naughty", "Neat", "Nerdy", "Nibbly", "Nifty", "Nimble",
	"Noble", "Noisy", "Nosy", "Nutty", "Odd", "Oily", "Pearly",
	"Peppy", "Perky", "Pesky", "Plucky", "Plumpy", "Pointy", "Poky",
	"Polite", "Posh", "Pouty", "Prickly", "Proud", "Puffy", "Pungent",
	"Quaint", "Queasy", "Quick", "Quiet", "Quirky", "Quizzical", "Rapid",
	"Rascally", "Roaming", "Roasty", "Rosy", "Rowdy", "Royal", "Ruffled",
	"Rumbly", "Rusty", "Sassy", "Sauntering", "Scrappy", "Scruffy", "Shaggy",
	"Shaky", "Sharp", "Shifty", "Shiny", "Shivery", "Shy", "Silky",
	"Silly", "Sincere", "Sketchy", "Skittish", "Sleepy", "Sleek", "Slick",
	"Slimy", "Slinky", "Sloppy", "Slow", "Sluggish", "Sly", "Smelly",
	"Smiley", "Smirky", "Smoky", "Smug", "Snappy", "Sneaky", "Sniffly",
	"Snoozy", "Snooty", "Snorty", "Snuggly", "Soggy", "Sparkly", "Speedy",
	"Spiky", "Spongy", "Spooky", "Spotty", "Springy", "Squeaky", "Squiggly",
	"Squishy", "Starry", "Steamy", "Stinky", "Stompy", "Stormy", "Stretchy",
	"Stubby", "Sturdy", "Subtle", "Sunny", "Sweaty", "Sweet", "Swift",
	"Tangy", "Tasty", "Tender", "Thirsty", "Thrifty", "Thumpy", "Ticklish",
	"Tidy", "Tiny", "Tipsy", "Toasty", "Toothy", "Tricky", "Trusty",
	"Tubby", "Twinkly", "Twirly", "Twitchy", "Vivid", "Wacky", "Waddly",
	"Warm", "Wary", "Weary", "Weepy", "Weird", "Whiny", "Whirly",
	"Whisky", "Wibbly", "Wiggly", "Wild", "Willowy", "Windy", "Winsome",
	"Wise", "Witty", "Wobbly", "Wonky", "Woolly", "Woozy", "Yawning",
	"Yummy", "Zany", "Zealous", "Zesty", "Zippy",
}

// petnameNouns is a curated list of mostly animals with a sprinkle of
// fantastical / edible / cosmic objects. Title-cased to match adjectives.
//
//nolint:gochecknoglobals // dictionary table; values never mutate.
var petnameNouns = []string{
	"Aardvark", "Albatross", "Alpaca", "Antelope", "Ape", "Armadillo",
	"Asteroid", "Avocado", "Badger", "Banana", "Barnacle", "Bat",
	"Beaver", "Beetle", "Biscuit", "Bison", "Blobfish", "Boar",
	"Bobcat", "Buffalo", "Bullfrog", "Bumblebee", "Bunny", "Butterfly",
	"Cactus", "Camel", "Capybara", "Caribou", "Cat", "Caterpillar",
	"Catfish", "Centaur", "Chameleon", "Cheetah", "Chickadee", "Chimera",
	"Chinchilla", "Chipmunk", "Cobra", "Comet", "Cookie", "Coyote",
	"Crab", "Crane", "Cricket", "Crocodile", "Crow", "Cupcake",
	"Cuttlefish", "Cyclops", "Dingo", "Dodo", "Dolphin", "Donkey",
	"Doughnut", "Dragon", "Dragonfly", "Duck", "Dumpling", "Eagle",
	"Eel", "Elephant", "Elk", "Emu", "Ferret", "Finch", "Firefly",
	"Flamingo", "Fox", "Frog", "Galaxy", "Gazelle", "Gecko", "Gerbil",
	"Ghost", "Giraffe", "Gnome", "Goblin", "Goldfish", "Goose",
	"Gopher", "Gorilla", "Grasshopper", "Griffin", "Hamster", "Hare",
	"Hawk", "Hedgehog", "Heron", "Hippo", "Hornet", "Hummingbird",
	"Hyena", "Iguana", "Impala", "Jackal", "Jackrabbit", "Jaguar",
	"Jellyfish", "Kangaroo", "Kingfisher", "Kiwi", "Koala", "Kraken",
	"Lemming", "Lemur", "Leopard", "Lion", "Lizard", "Llama", "Lobster",
	"Lynx", "Macaw", "Magpie", "Mammoth", "Manatee", "Mantis", "Marmot",
	"Meerkat", "Mole", "Mongoose", "Monkey", "Moose", "Moth", "Muffin",
	"Mule", "Narwhal", "Nebula", "Newt", "Nightingale", "Octopus",
	"Okapi", "Opossum", "Orangutan", "Ostrich", "Otter", "Owl", "Oyster",
	"Panda", "Pancake", "Pangolin", "Parrot", "Peacock", "Pelican",
	"Penguin", "Phoenix", "Piglet", "Platypus", "Porcupine", "Possum",
	"Pufferfish", "Puffin", "Pug", "Pumpkin", "Quail", "Quokka",
	"Rabbit", "Raccoon", "Ram", "Raven", "Reindeer", "Rhino", "Robin",
	"Salamander", "Sandwich", "Sardine", "Scorpion", "Seal", "Seahorse",
	"Shark", "Sheep", "Shrew", "Shrimp", "Skunk", "Sloth", "Snail",
	"Sparrow", "Sphinx", "Spider", "Squid", "Squirrel", "Starfish",
	"Stingray", "Stork", "Sunfish", "Swan", "Tadpole", "Tapir", "Tiger",
	"Toad", "Tortoise", "Toucan", "Turkey", "Turtle", "Unicorn", "Viper",
	"Vulture", "Walrus", "Warthog", "Wasp", "Weasel", "Whale", "Wolf",
	"Wolverine", "Wombat", "Woodpecker", "Yak", "Zebra",
}
