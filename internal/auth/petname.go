package auth

import (
	"crypto/rand"
	"errors"
	"math/big"
)

// GeneratePetname returns an "Adjective-Adjective-Noun" identifier
// (e.g. "Clever-Quick-Quizzer"). Not guaranteed unique - callers handle
// the UNIQUE collision with a retry loop.
func GeneratePetname() string {
	a1 := pickRandom(petnameAdjectives)
	a2 := pickRandom(petnameAdjectives)
	n := pickRandom(petnameNouns)

	return a1 + "-" + a2 + "-" + n
}

// CreateWithPetnameFallback calls create with firstChoice and, on an
// ErrDisplayNameTaken collision, retries with a freshly generated
// petname up to maxPetnameAttempts times. Any other error, or success,
// returns immediately. The error is returned unwrapped so each caller
// keeps its own wrapping.
func CreateWithPetnameFallback(
	firstChoice string,
	create func(name string) (*Player, error),
) (*Player, error) {
	const maxPetnameAttempts = 5

	name := firstChoice
	var err error
	for range maxPetnameAttempts + 1 {
		var player *Player
		player, err = create(name)
		if err == nil {
			return player, nil
		}
		if !errors.Is(err, ErrDisplayNameTaken) {
			return nil, err
		}
		name = GeneratePetname()
	}

	return nil, err
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

// petnameAdjectives is the title-cased dictionary of knowledge- and
// wit-themed words used by GeneratePetname.
//
//nolint:gochecknoglobals // dictionary table; values never mutate.
var petnameAdjectives = []string{
	"Able", "Academic", "Adept", "Adroit", "Alert", "Analytical", "Apt",
	"Ardent", "Artful", "Assured", "Astute", "Attentive", "Avid", "Aware",
	"Bold", "Bookish", "Brainy", "Bright", "Brilliant", "Calculating", "Canny",
	"Capable", "Careful", "Cerebral", "Certain", "Cheeky", "Chipper", "Clear",
	"Clever", "Clued", "Cogent", "Cognitive", "Cognizant", "Coherent",
	"Composed", "Confident", "Considered", "Contemplative", "Cool", "Correct",
	"Crafty", "Crisp", "Cultured", "Cunning", "Curious", "Daring", "Dazzling",
	"Decisive", "Dedicated", "Deductive", "Deft", "Deliberate", "Determined",
	"Devoted", "Diligent", "Diplomatic", "Discerning", "Distinguished",
	"Dogged", "Driven", "Droll", "Eager", "Educated", "Effective", "Efficient",
	"Eloquent", "Encyclopedic", "Enlightened", "Enterprising", "Erudite",
	"Exact", "Expert", "Fabled", "Fair", "Famed", "Fervent", "Fitting",
	"Fluent", "Focused", "Foxy", "Frank", "Genuine", "Gifted", "Gleaming",
	"Glowing", "Graceful", "Grand", "Gritty", "Handy", "Hardy", "Honest",
	"Humble", "Incisive", "Informed", "Ingenious", "Inquiring", "Inquisitive",
	"Insightful", "Instant", "Intent", "Intuitive", "Inventive", "Jaunty",
	"Jovial", "Judicious", "Keen", "Knowing", "Knowledgeable", "Learned",
	"Lettered", "Level", "Limber", "Lively", "Logical", "Lucid", "Lucky",
	"Luminous", "Masterful", "Matchless", "Measured", "Methodical",
	"Meticulous", "Mindful", "Modest", "Motivated", "Neat", "Nifty", "Nimble",
	"Noble", "Notable", "Objective", "Observant", "Orderly", "Original",
	"Owlish", "Patient", "Peerless", "Pensive", "Peppy", "Perceptive",
	"Percipient", "Perky", "Persistent", "Perspicacious", "Philosophical",
	"Pithy", "Playful", "Plucky", "Poignant", "Poised", "Polished", "Positive",
	"Practical", "Practiced", "Precise", "Premier", "Prepared", "Prescient",
	"Primed", "Prized", "Probing", "Prodigious", "Proficient", "Prompt",
	"Prudent", "Puckish", "Punctual", "Quick", "Quiet", "Quippy", "Quizzical",
	"Radiant", "Rapid", "Rational", "Ready", "Reasoned", "Refined",
	"Reflective", "Reliable", "Renowned", "Resolute", "Resourceful", "Rigorous",
	"Sagacious", "Sage", "Salient", "Sapient", "Sassy", "Savvy", "Scholarly",
	"Scholastic", "Scrappy", "Searching", "Selective", "Sensible", "Sensitive",
	"Sharp", "Shining", "Shrewd", "Sincere", "Skilled", "Skillful", "Slick",
	"Sly", "Smart", "Smooth", "Snappy", "Solid", "Sound", "Sparkling",
	"Speculative", "Speedy", "Spirited", "Sprightly", "Spry", "Stable",
	"Steadfast", "Steady", "Stellar", "Sterling", "Storied", "Streetwise",
	"Striking", "Studied", "Studious", "Sturdy", "Suave", "Subtle", "Supple",
	"Supreme", "Sure", "Swift", "Systematic", "Tactful", "Tactical", "Talented",
	"Tempered", "Tenacious", "Thorough", "Thoughtful", "Tidy", "Trained",
	"Trim", "Trusty", "Truthful", "Uncanny", "Undaunted", "Unerring", "Upbeat",
	"Valiant", "Versed", "Veteran", "Vibrant", "Victorious", "Vigilant",
	"Vital", "Vivacious", "Vivid", "Wary", "Watchful", "Whimsical", "Willing",
	"Wily", "Winning", "Wise", "Witty", "Wizardly", "Wondering", "Worldly",
	"Worthy", "Zealous", "Zesty", "Zippy",
}

// petnameNouns is the title-cased dictionary of quiz-, question-, and
// winning-themed words used by GeneratePetname.
//
//nolint:gochecknoglobals // dictionary table; values never mutate.
var petnameNouns = []string{
	"Ace", "Adept", "Almanac", "Analyst", "Answer", "Answerer", "Arbiter",
	"Archive", "Atlas", "Award", "Axiom", "Badge", "Beacon", "Beaver", "Bee",
	"Bell", "Blueprint", "Board", "Boffin", "Bonanza", "Bonus", "Book",
	"Bookworm", "Bracket", "Brain", "Brainbox", "Brainiac", "Buff", "Bulb",
	"Buzzer", "Candle", "Champ", "Champion", "Chart", "Chronicle", "Cipher",
	"Clue", "Codebreaker", "Codex", "Combo", "Compass", "Compendium",
	"Contender", "Contest", "Contestant", "Conundrum", "Cornerstone",
	"Crackerjack", "Crown", "Cue", "Cup", "Datum", "Detective", "Dictionary",
	"Doctor", "Dossier", "Egghead", "Encoder", "Encyclopedia", "Enigma",
	"Equation", "Exam", "Examiner", "Expert", "Fact", "Factoid", "Falcon",
	"Final", "Finalist", "Flame", "Formula", "Fox", "Genius", "Glossary",
	"Guesser", "Guru", "Handbook", "Hint", "Index", "Insight", "Investigator",
	"Jackpot", "Journal", "Judge", "Key", "Keystone", "Knower", "Ladder",
	"Lantern", "Laureate", "Laurel", "Ledger", "Lexicon", "Library",
	"Lightbulb", "Logician", "Lore", "Maestro", "Manual", "Map", "Marshal",
	"Master", "Mastermind", "Maven", "Medal", "Mentor", "Milestone", "Mystery",
	"Notebook", "Octopus", "Oracle", "Overachiever", "Owl", "Pacesetter",
	"Pathfinder", "Pattern", "Philosopher", "Pinnacle", "Player", "Podium",
	"Point", "Polymath", "Poser", "Primer", "Prize", "Prizewinner", "Prodigy",
	"Professor", "Prompt", "Proof", "Pundit", "Puzzle", "Puzzler", "Quandary",
	"Query", "Quest", "Quester", "Question", "Quiz", "Quizmaster", "Quizzer",
	"Rank", "Raven", "Reasoner", "Record", "Referee", "Register", "Ribbon",
	"Riddle", "Riddler", "Rosette", "Round", "Rule", "Sage", "Sash", "Savant",
	"Scholar", "Score", "Scoreboard", "Scribe", "Scroll", "Sensation",
	"Serpent", "Sleuth", "Solver", "Sorcerer", "Spark", "Speculator", "Sphinx",
	"Stage", "Standout", "Strategist", "Streak", "Summit", "Tactician", "Tally",
	"Teaser", "Test", "Theorem", "Thinker", "Titan", "Title", "Tome", "Torch",
	"Trailblazer", "Trendsetter", "Triumph", "Trivia", "Trophy", "Tutor",
	"Umpire", "Vanguard", "Vault", "Verdict", "Veteran", "Victor", "Victory",
	"Virtuoso", "Volume", "Whiz", "Whizkid", "Winner", "Wisdom", "Wit",
	"Wizard", "Wonder", "Wordsmith", "Wreath", "Wunderkind", "Zenith",
}
