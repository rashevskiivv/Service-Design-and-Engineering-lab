import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;

/** Lab 6: a small library loan system. */
public class Library {

    static class Book {
        final String isbn;
        final String title;
        final double dailyFee;

        Book(String isbn, String title, double dailyFee) {
            this.isbn = isbn;
            this.title = title;
            this.dailyFee = dailyFee;
        }
    }

    static class Loan {
        final Book book;
        final String member;
        final int days;

        Loan(Book book, String member, int days) {
            this.book = book;
            this.member = member;
            this.days = days;
        }

        int cost() {
            return book.dailyFee * days;
        }
    }

    private final Map<String, Book> catalog = new HashMap<>();
    private final List<Loan> loans = new ArrayList<>();

    void addBook(Book book) {
        catalog.put(book.isbn, book);
    }

    /** Lends a book if it exists and is not already on loan. */
    boolean lend(String isbn, String member, int days) {
        Book book = catalog.get(isbn);
        if (book == null) {
            return false;
        }
        for (Loan loan : loans) {
            if (loan.book.isbn.equals(isbn)) {
                return false;
            }
        }
        loans.add(new Loan(book, member, days));
        return true;
    }

    Map<String, Integer> totalsByMember() {
        Map<String, Integer> totals = new HashMap<>();
        for (Loan loan : loans) {
            totals.merge(loan.member, loan.cost(), Integer::sum);
        }
        return totals;
    }

    public static void main(String[] args) {
        Library lib = new Library();
        lib.addBook(new Book("978-0134685991", "Effective Java", 0.50));
        lib.addBook(new Book("978-0262033848", "Introduction to Algorithms", 0.75));
        System.out.println(lib.lend("978-0134685991", "anna", 14));
        System.out.println(lib.lend("978-0262033848", "marco", 7));
        System.out.println(lib.lend("978-0134685991", "marco", 3));
        System.out.println(lib.totalsByMember());
    }
}
