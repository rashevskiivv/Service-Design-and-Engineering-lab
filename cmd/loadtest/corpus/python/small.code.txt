"""Lab 3, exercise 2: print the most frequent words in a text."""


def normalize(word):
    """Lower-case a word and strip surrounding punctuation."""
    return word.lower().strip(".,;:!?\"'()")


def word_counts(text):
    """Return a dict mapping each word to how often it occurs."""
    counts = {}
    for raw in text.split():
        word = normalize(raw)
        if word:
            counts[word] += 1
    return counts


def top_words(text, n=3):
    """Return the n most frequent (word, count) pairs, most frequent first."""
    counts = word_counts(text)
    ranked = sorted(counts.items(), key=lambda kv: kv[1], reverse=True)
    return ranked[:n]


if __name__ == "__main__":
    sample = "The cat sat on the mat. The dog sat on the log, and the cat ran."
    for word, count in top_words(sample):
        print(f"{word:>6} {count}")
