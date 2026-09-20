// Form dates use the browser timezone, exactly like manual edits in these apps.
window.PrismPIM = (() => {
  const pad = n => String(n).padStart(2,'0');
  const day = d => `${d.getFullYear()}-${pad(d.getMonth()+1)}-${pad(d.getDate())}`;
  const local = d => `${day(d)}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
  function date(value) {
    if (typeof value !== 'string' || !/^\d{4}-\d{2}-\d{2}(?:T\d{2}:\d{2}(?::\d{2}(?:\.\d+)?)?(?:Z|[+-]\d{2}:\d{2})?)?$/.test(value)) throw Error('Use YYYY-MM-DD or an ISO date/time.');
    const raw = value.slice(0,10), check = new Date(raw+'T12:00:00');
    if (!Number.isFinite(+check) || day(check) !== raw) throw Error('Invalid date.');
    const d = new Date(value.length===10 ? value+'T00:00:00' : value);
    if (!Number.isFinite(+d)) throw Error('Invalid date/time.');
    if (value.length>10 && !/(Z|[+-]\d{2}:\d{2})$/.test(value) && local(d)!==value.slice(0,16)) throw Error('This local time does not exist in the browser timezone.');
    return d;
  }
  function shift(value, days) { const d=date(value);d.setDate(d.getDate()+days);return day(d); }
  function taskPatch(current, patch, dateOnly) {
    const next={...current,...patch};
    if (!['low','normal','high'].includes(next.priority)) throw Error('Priority must be low, normal or high.');
    if (next.due) { const d=date(next.due);next.due=dateOnly?day(d):local(d); }
    return next;
  }
  function calendarPatch(current, patch) {
    const next={...current,...patch}, all=next.all_day;
    let baseStart=current.start,baseEnd=current.end;
    if(all!==current.all_day){
      baseStart=all?day(date(baseStart)):baseStart+'T09:00';
      baseEnd=all?(baseEnd?shift(day(date(baseEnd)),1):shift(baseStart,1)):(baseEnd?shift(baseEnd,-1)+'T10:00':local(new Date(+date(baseStart)+3600000)));
    }
    next.start=patch.start ?? baseStart;
    next.end=patch.end ?? baseEnd;
    const format=v=>all?day(date(v)):local(date(v));
    next.start=format(next.start);
    if ('start' in patch && !('end' in patch)) {
      if(all){const days=baseEnd?Math.max(1,Math.round((Date.parse(day(date(baseEnd)))-Date.parse(day(date(baseStart))))/86400000)):1;next.end=shift(next.start,days);}
      else {const duration=baseEnd?+date(baseEnd)-+date(baseStart):3600000;next.end=local(new Date(+date(next.start)+Math.max(60000,duration)));}
    }
    if(next.end)next.end=format(next.end);
    if(next.end && +date(next.end)<=+date(next.start))throw Error('End must be after start.');
    return next;
  }
  return {date,day,local,shift,taskPatch,calendarPatch,timezone:()=>Intl.DateTimeFormat().resolvedOptions().timeZone};
})();
