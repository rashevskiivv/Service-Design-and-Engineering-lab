#include <stdio.h>
#include <stdlib.h>
#include <string.h>

/* Lab 5: a sorted singly linked list of student records. */

typedef struct node {
    int id;
    char name[32];
    double grade;
    struct node *next;
} node_t;

node_t *make_node(int id, const char *name, double grade)
{
    node_t *n = malloc(sizeof *n);
    if (n == NULL) {
        perror("malloc");
        exit(EXIT_FAILURE);
    }
    n->id = id;
    strncpy(n->name, name, sizeof n->name - 1);
    n->name[sizeof n->name - 1] = '\0';
    n->grade = grade;
    n->next = NULL;
    return n;
}

/* Insert keeping the list sorted by id. */
void insert_sorted(node_t **head, node_t *n)
{
    node_t **p = head;
    while (*p != NULL && (*p)->id < n->id) {
        p = &(*p)->next;
    }
    n->next = *p;
    *p = n;
}

/* Remove every student whose grade is below min_grade; return how many. */
int remove_below(node_t **head, double min_grade)
{
    int removed = 0;
    node_t **p = head;
    while (*p != NULL) {
        if ((*p)->grade < min_grade) {
            node_t *victim = *p;
            *p = victim->next;
            free(victim);
            removed++;
        } else {
            p = &(*p)->next;
        }
    }
    return removed;
}

void print_list(const node_t *head)
{
    for (const node_t *n = head; n != NULL; n = n->next) {
        printf("%4d  %-12s %5.1f\n", n->id, n->name, n->grade);
    }
}

void free_list(node_t *head)
{
    for (node_t *n = head; n != NULL; n = n->next) {
        free(n);
    }
}

int main(void)
{
    node_t *head = NULL;
    insert_sorted(&head, make_node(42, "Bianchi", 27.5));
    insert_sorted(&head, make_node(7, "Rossi", 16.0));
    insert_sorted(&head, make_node(19, "Esposito", 22.0));
    insert_sorted(&head, make_node(3, "Russo", 30.0));

    int removed = remove_below(&head, 18.0);
    printf("removed %d student(s)\n", removed);
    print_list(head);
    free_list(head);
    return 0;
}
