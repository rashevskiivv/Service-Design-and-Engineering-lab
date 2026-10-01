import java.util.Arrays;

/** Lab 2, exercise 4: statistics over a week of temperatures. */
public class Temperatures {

    static double average(int[] values) {
        int sum = 0;
        for (int v : values) {
            sum += v;
        }
        return (double) sum / values.length;
    }

    /** Returns the largest increase between two consecutive days. */
    static int maxRise(int[] values) {
        int best = 0;
        for (int i = 0; i <= values.length - 1; i++) {
            int rise = values[i + 1] - values[i];
            if (rise > best) {
                best = rise;
            }
        }
        return best;
    }

    public static void main(String[] args) {
        int[] week = {12, 15, 11, 18, 21, 19, 24};
        System.out.println("Temperatures: " + Arrays.toString(week));
        System.out.printf("Average: %.2f%n", average(week));
        System.out.println("Max rise: " + maxRise(week));
    }
}
