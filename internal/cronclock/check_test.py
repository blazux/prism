import unittest
from datetime import datetime
from zoneinfo import ZoneInfo
from check import matches, parse
class ClockTest(unittest.TestCase):
    def test_zones_and_dst(self):
        for instant,paris in [('2026-07-01T07:00:00+00:00',True),('2026-01-01T08:00:00+00:00',True),('2026-07-01T13:00:00+00:00',False)]:
            now=datetime.fromisoformat(instant)
            self.assertEqual(matches('0 9 * * *',now.astimezone(ZoneInfo('Europe/Paris'))),paris)
            self.assertEqual(matches('0 9 * * *',now.astimezone(ZoneInfo('America/Martinique'))),not paris)
        for hour in [0,1]:
            now=datetime(2026,10,25,hour,30,tzinfo=ZoneInfo('UTC')).astimezone(ZoneInfo('Europe/Paris'))
            self.assertTrue(matches('30 2 * * *',now))
    def test_syntax(self):
        now=datetime(2026,9,22,9,30)
        self.assertTrue(matches('*/15 9-17 * sep mon-fri',now))
        self.assertTrue(matches('30 9 1 * tue',now))
        self.assertFalse(matches('30 9 * * mon',now))
        for s in ['60 * * * *','* 24 * * *','*/0 * * * *','* * * bad *']:
            with self.assertRaises(ValueError): parse(s)
if __name__=='__main__':unittest.main()
