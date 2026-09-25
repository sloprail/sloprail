def average(numbers):
    total = 0
    for n in numbers:
        total += n
    return total / len(numbers)


def percent_change(old, new):
    # Bug: division by old, but doesn't guard against old == 0
    return (new - old) / old * 100
