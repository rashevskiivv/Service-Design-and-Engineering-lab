"""Lab 5: a tiny gradebook that reads grades from CSV text and prints a report."""

import csv
import io
import statistics
from dataclasses import dataclass, field


@dataclass
class Student:
    student_id: str
    name: str
    grades: list = field(default_factory=list)

    def add_grade(self, grade):
        if not 0 <= grade <= 30:
            raise ValueError(f"grade out of range: {grade}")
        self.grades.append(grade)

    def average(self):
        return sum(self.grades) / len(self.grades)

    def passed(self):
        return self.average() >= 18


class Gradebook:
    def __init__(self):
        self.students = {}

    def load_csv(self, text):
        """Rows are student_id,name,grade; grade is empty if the exam was skipped."""
        reader = csv.DictReader(io.StringIO(text))
        for row in reader:
            sid = row["student_id"].strip()
            student = self.students.get(sid)
            if student is None:
                student = Student(sid, row["name"].strip())
                self.students[sid] = student
            grade = row["grade"].strip()
            if grade:
                student.add_grade(int(grade))

    def ranking(self):
        return sorted(self.students.values(), key=lambda s: s.average(), reverse=True)

    def report(self):
        lines = []
        for position, student in enumerate(self.ranking(), start=1):
            status = "PASS" if student.passed() else "FAIL"
            lines.append(
                f"{position:2d}. {student.name:<10} avg {student.average():5.2f}  {status}"
            )
        averages = [s.average() for s in self.students.values()]
        lines.append(f"class median: {statistics.median(averages):.2f}")
        return "\n".join(lines)


DATA = """student_id,name,grade
s001,Anna,28
s002,Marco,17
s001,Anna,30
s003,Luca,
s002,Marco,21
s004,Sofia,25
"""

if __name__ == "__main__":
    book = Gradebook()
    book.load_csv(DATA)
    print(book.report())
