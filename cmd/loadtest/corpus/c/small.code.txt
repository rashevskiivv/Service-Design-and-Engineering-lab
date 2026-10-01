#include <stdio.h>
#include <string.h>

/* Lab 2, exercise 3: print a name as "Last, First" and count its vowels. */

void format_name(const char *first, const char *last, char *out)
{
    strcpy(out, last);
    strcat(out, ", ");
    strcat(out, first);
}

int count_vowels(const char *s)
{
    int n = 0;
    for (size_t i = 0; i < strlen(s); i++) {
        if (strchr("aeiouAEIOU", s[i]) != NULL) {
            n++;
        }
    }
    return n;
}

int main(void)
{
    const char *first = "Margherita";
    const char *last = "Montalcini";
    char full[16];

    format_name(first, last, full);
    printf("%s (%d vowels)\n", full, count_vowels(full));
    return 0;
}
