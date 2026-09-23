"""Exit 0 when the current minute matches a cron expression in an IANA zone.
No command execution, network access, configuration or credentials here.
"""
import sys
from datetime import datetime
from zoneinfo import ZoneInfo

ALIASES = {'@yearly': '0 0 1 1 *', '@annually': '0 0 1 1 *',
           '@monthly': '0 0 1 * *', '@weekly': '0 0 * * 0',
           '@daily': '0 0 * * *', '@midnight': '0 0 * * *', '@hourly': '0 * * * *'}
MONTHS = dict(zip(['jan','feb','mar','apr','may','jun','jul','aug','sep','oct','nov','dec'], range(1,13)))
DAYS = dict(zip(['sun','mon','tue','wed','thu','fri','sat'], range(7)))

def field(text, low, high, names=None):
    names = names or {}
    def value(v):
        n = names[v.lower()] if v.lower() in names else int(v)
        if not low <= n <= high:
            raise ValueError('cron value out of range')
        return n
    result = set()
    for term in text.split(','):
        part, sep, step_text = term.partition('/')
        step = int(step_text) if sep else 1
        if step <= 0:
            raise ValueError('cron step must be positive')
        if part == '*':
            first, last = low, high
        elif '-' in part:
            a, b = part.split('-')
            first, last = value(a), value(b)
        else:
            first = value(part)
            last = high if sep else first
        if first > last:
            raise ValueError('reversed cron range')
        result.update(range(first, last + 1, step))
    return result

def parse(schedule):
    parts = ALIASES.get(schedule, schedule).split()
    if len(parts) != 5:
        raise ValueError('cron requires five fields')
    values = [field(parts[0], 0, 59), field(parts[1], 0, 23),
              field(parts[2], 1, 31), field(parts[3], 1, 12, MONTHS),
              field(parts[4], 0, 7, DAYS)]
    if 7 in values[4]:
        values[4].add(0)
    return parts, values

def matches(schedule, now):
    parts, values = parse(schedule)
    dom = now.day in values[2]
    dow = (now.weekday() + 1) % 7 in values[4]
    day = (dom and dow) if parts[2].startswith('*') or parts[4].startswith('*') else (dom or dow)
    return now.minute in values[0] and now.hour in values[1] and now.month in values[3] and day

if __name__ == '__main__':
    try:
        schedule, zone = sys.argv[1:3]
        loc = ZoneInfo(zone)
        parse(schedule)
        if len(sys.argv) > 3 and sys.argv[3] == '--check':
            sys.exit(0)
        sys.exit(0 if matches(schedule, datetime.now(loc)) else 1)
    except (ValueError, KeyError, IndexError) as error:
        print('Invalid cron timezone or schedule: ' + str(error), file=sys.stderr)
        sys.exit(2)
