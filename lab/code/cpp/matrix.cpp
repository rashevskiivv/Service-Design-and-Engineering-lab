#include <cstddef>
#include <iostream>
#include <stdexcept>

// Lab 6: a dense matrix class with addition and multiplication.

class Matrix {
public:
    Matrix(std::size_t rows, std::size_t cols)
        : rows_(rows), cols_(cols), data_(new double[rows * cols]()) {}

    ~Matrix() { delete[] data_; }

    std::size_t rows() const { return rows_; }
    std::size_t cols() const { return cols_; }

    double& at(std::size_t r, std::size_t c) {
        if (r >= rows_ || c >= cols_) throw std::out_of_range("Matrix::at");
        return data_[r * cols_ + c];
    }

    double at(std::size_t r, std::size_t c) const {
        if (r >= rows_ || c >= cols_) throw std::out_of_range("Matrix::at");
        return data_[r * cols_ + c];
    }

    Matrix operator+(const Matrix& other) const {
        if (rows_ != other.rows_ || cols_ != other.cols_) {
            throw std::invalid_argument("size mismatch in +");
        }
        Matrix result(rows_, cols_);
        for (std::size_t i = 0; i < rows_ * cols_; ++i) {
            result.data_[i] = data_[i] + other.data_[i];
        }
        return result;
    }

    Matrix operator*(const Matrix& other) const {
        if (cols_ != other.rows_) {
            throw std::invalid_argument("size mismatch in *");
        }
        Matrix result(rows_, other.cols_);
        for (std::size_t i = 0; i < rows_; ++i)
            for (std::size_t j = 0; j < other.cols_; ++j)
                for (std::size_t k = 0; k < cols_; ++k)
                    result.at(i, j) += at(i, k) * other.at(k, j);
        return result;
    }

    static Matrix identity(std::size_t n) {
        Matrix m(n, n);
        for (std::size_t i = 0; i < n; ++i) m.at(i, i) = 1.0;
        return m;
    }

private:
    std::size_t rows_;
    std::size_t cols_;
    double* data_;
};

std::ostream& operator<<(std::ostream& os, const Matrix& m) {
    for (std::size_t r = 0; r < m.rows(); ++r) {
        for (std::size_t c = 0; c < m.cols(); ++c) os << m.at(r, c) << ' ';
        os << '\n';
    }
    return os;
}

int main() {
    Matrix a(2, 2);
    a.at(0, 0) = 1; a.at(0, 1) = 2;
    a.at(1, 0) = 3; a.at(1, 1) = 4;

    Matrix b = a;
    Matrix c = a + Matrix::identity(2);
    std::cout << c << a * b;
    return 0;
}
