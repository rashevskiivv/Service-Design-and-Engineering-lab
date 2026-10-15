#include <iostream>
#include <stdexcept>
#include <vector>

// Lab 3, exercise 5: a bounded stack of integers.

class Stack {
public:
    explicit Stack(std::size_t capacity) : capacity_(capacity) {}

    void push(int value) {
        if (items_.size() == capacity_) {
            throw std::overflow_error("stack is full");
        }
        items_.push_back(value);
    }

    int pop() {
        if (items_.empty()) {
            throw std::underflow_error("stack is empty");
        }
        int top = items_.back();
        items_.pop_back();
        return top;
    }

    std::size_t size() { return items_.size(); }

private:
    std::vector<int> items_;
    std::size_t capacity_;
};

void report(const Stack& s) {
    std::cout << "stack holds " << s.size() << " item(s)\n";
}

int main() {
    Stack s(3);
    s.push(1);
    s.push(2);
    report(s);
    std::cout << s.pop() << '\n';
    return 0;
}
